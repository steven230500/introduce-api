package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type contextKey string

const UserIDKey contextKey = "user_id"

type jwksKey struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

type SupabaseVerifier struct {
	jwksURL   string
	mu        sync.RWMutex
	keys      []jwksKey
	fetchedAt time.Time
}

func NewSupabaseVerifier(supabaseURL string) (*SupabaseVerifier, error) {
	v := &SupabaseVerifier{
		jwksURL: strings.TrimRight(supabaseURL, "/") + "/auth/v1/.well-known/jwks.json",
	}
	if err := v.refresh(); err != nil {
		return nil, fmt.Errorf("jwks fetch: %w", err)
	}
	return v, nil
}

func (v *SupabaseVerifier) refresh() error {
	resp, err := http.Get(v.jwksURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var payload struct {
		Keys []jwksKey `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return err
	}

	v.mu.Lock()
	v.keys = payload.Keys
	v.fetchedAt = time.Now()
	v.mu.Unlock()
	return nil
}

func (v *SupabaseVerifier) keyFunc(t *jwt.Token) (any, error) {
	v.mu.RLock()
	keys := v.keys
	fetched := v.fetchedAt
	v.mu.RUnlock()

	if time.Since(fetched) > time.Hour {
		_ = v.refresh()
		v.mu.RLock()
		keys = v.keys
		v.mu.RUnlock()
	}

	kid, _ := t.Header["kid"].(string)
	for _, k := range keys {
		if kid != "" && k.Kid != kid {
			continue
		}
		if k.Kty == "EC" {
			return decodeECKey(k)
		}
	}
	return nil, fmt.Errorf("no matching key for kid=%s", kid)
}

func decodeECKey(k jwksKey) (*ecdsa.PublicKey, error) {
	xb, err := base64.RawURLEncoding.DecodeString(k.X)
	if err != nil {
		return nil, err
	}
	yb, err := base64.RawURLEncoding.DecodeString(k.Y)
	if err != nil {
		return nil, err
	}
	var curve elliptic.Curve
	switch k.Crv {
	case "P-256":
		curve = elliptic.P256()
	case "P-384":
		curve = elliptic.P384()
	case "P-521":
		curve = elliptic.P521()
	default:
		return nil, fmt.Errorf("unsupported curve: %s", k.Crv)
	}
	return &ecdsa.PublicKey{
		Curve: curve,
		X:     new(big.Int).SetBytes(xb),
		Y:     new(big.Int).SetBytes(yb),
	}, nil
}

func (v *SupabaseVerifier) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		tokenStr := strings.TrimPrefix(header, "Bearer ")

		token, err := jwt.Parse(tokenStr, v.keyFunc,
			jwt.WithValidMethods([]string{"ES256", "ES384", "ES512"}),
		)
		if err != nil || !token.Valid {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		claims, _ := token.Claims.(jwt.MapClaims)
		sub, _ := claims["sub"].(string)
		ctx := context.WithValue(r.Context(), UserIDKey, sub)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func UserID(r *http.Request) string {
	v, _ := r.Context().Value(UserIDKey).(string)
	return v
}
