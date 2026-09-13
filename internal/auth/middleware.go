package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/steven230500/introduce-api/internal/httpx"
)

type contextKey string

const (
	userIDKey contextKey = "user_id"
	orgIDKey  contextKey = "org_id"
)

// Middleware rejects any request without a valid access token, and puts the
// caller's identity on the context for handlers downstream.
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := bearerToken(r)
		if raw == "" {
			httpx.WriteError(w, httpx.ErrUnauthorized)
			return
		}

		claims, err := s.tokens.ParseAccess(raw)
		if err != nil {
			httpx.WriteError(w, httpx.Fail(
				http.StatusUnauthorized, "invalid_token", "sesión inválida o vencida"))
			return
		}

		userID, err := uuid.Parse(claims.Subject)
		if err != nil {
			httpx.WriteError(w, httpx.ErrUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), userIDKey, userID)
		if claims.OrgID != "" {
			if orgID, err := uuid.Parse(claims.OrgID); err == nil {
				ctx = context.WithValue(ctx, orgIDKey, orgID)
			}
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireOrg rejects a caller whose token carries no organization.
//
// Every content route is org-scoped, so a token minted before the user joined
// one must not reach a handler that would otherwise query with a zero uuid.
func RequireOrg(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := OrgID(r.Context()); !ok {
			httpx.WriteError(w, httpx.Fail(
				http.StatusForbidden, "no_org", "la sesión no tiene organización activa"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// UserID returns the authenticated caller.
func UserID(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(userIDKey).(uuid.UUID)
	return id, ok
}

// OrgID returns the caller's active organization.
func OrgID(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(orgIDKey).(uuid.UUID)
	return id, ok
}

// MustOrgID is for handlers already behind RequireOrg.
func MustOrgID(ctx context.Context) uuid.UUID {
	id, _ := OrgID(ctx)
	return id
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if header == "" {
		// Websocket clients cannot set headers from the browser API, so the
		// token may also arrive as a query parameter on the upgrade request.
		return r.URL.Query().Get("access_token")
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

// WithIdentity puts a caller on a context the way [Service.Middleware] does,
// for code that already knows who is asking - a test driving a handler
// directly, without minting a token first.
func WithIdentity(ctx context.Context, userID, orgID uuid.UUID) context.Context {
	ctx = context.WithValue(ctx, userIDKey, userID)
	return context.WithValue(ctx, orgIDKey, orgID)
}
