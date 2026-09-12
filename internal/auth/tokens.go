package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Claims carried by an access token.
type Claims struct {
	jwt.RegisteredClaims
	OrgID string `json:"org,omitempty"`
}

// TokenIssuer mints and validates access tokens, and mints refresh tokens.
type TokenIssuer struct {
	secret     []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
}

func NewTokenIssuer(secret []byte, accessTTL, refreshTTL time.Duration) *TokenIssuer {
	return &TokenIssuer{secret: secret, accessTTL: accessTTL, refreshTTL: refreshTTL}
}

func (t *TokenIssuer) AccessTTL() time.Duration  { return t.accessTTL }
func (t *TokenIssuer) RefreshTTL() time.Duration { return t.refreshTTL }

// IssueAccess returns a signed, short-lived token for userID.
func (t *TokenIssuer) IssueAccess(userID uuid.UUID, orgID string) (string, error) {
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(t.accessTTL)),
			ID:        uuid.NewString(),
		},
		OrgID: orgID,
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(t.secret)
	if err != nil {
		return "", fmt.Errorf("sign access token: %w", err)
	}
	return signed, nil
}

// ParseAccess validates a token and returns its claims.
func (t *TokenIssuer) ParseAccess(raw string) (*Claims, error) {
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		// Pin the algorithm. Accepting whatever the token declares is how the
		// classic "alg: none" and RS256-to-HS256 forgeries get through.
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method %q", token.Method.Alg())
		}
		return t.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil {
		return nil, err
	}
	return claims, nil
}

// NewRefreshToken returns a random opaque token and the hash to store.
//
// Only the hash reaches the database, so a dump of refresh_tokens cannot be
// replayed against the API.
func NewRefreshToken() (token string, hash []byte, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("read refresh token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, HashRefreshToken(token), nil
}

// HashRefreshToken is the one-way function applied before storage and lookup.
func HashRefreshToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
