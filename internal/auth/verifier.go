package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

type contextKey string

const UserIDKey contextKey = "user_id"

type SupabaseVerifier struct {
	secret []byte
}

func NewSupabaseVerifier(secret string) *SupabaseVerifier {
	return &SupabaseVerifier{secret: []byte(secret)}
}

func (v *SupabaseVerifier) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		tokenStr := strings.TrimPrefix(header, "Bearer ")

		token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return v.secret, nil
		})
		if err != nil || !token.Valid {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		sub, _ := claims["sub"].(string)
		ctx := context.WithValue(r.Context(), UserIDKey, sub)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func UserID(r *http.Request) string {
	v, _ := r.Context().Value(UserIDKey).(string)
	return v
}
