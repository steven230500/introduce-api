package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/steven230500/introduce-api/internal/httpx"
)

// Service holds the sign-in rules.
type Service struct {
	repo   *Repo
	tokens *TokenIssuer
}

func NewService(repo *Repo, tokens *TokenIssuer) *Service {
	return &Service{repo: repo, tokens: tokens}
}

// Session is what a client receives after signing in or refreshing.
type Session struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	User         User   `json:"user"`
	OrgID        string `json:"org_id,omitempty"`
}

var errInvalidCredentials = httpx.Fail(
	http.StatusUnauthorized, "invalid_credentials", "correo o contraseña incorrectos")

// Register creates an account and signs it in.
func (s *Service) Register(ctx context.Context, email, password string, displayName *string) (Session, error) {
	email = normalizeEmail(email)
	if err := validateCredentials(email, password); err != nil {
		return Session{}, err
	}

	hash, err := HashPassword(password)
	if err != nil {
		return Session{}, err
	}

	user, err := s.repo.CreateUser(ctx, email, hash, displayName)
	if err != nil {
		// 23505 is unique_violation, which here can only be the email index.
		if strings.Contains(err.Error(), "23505") {
			return Session{}, httpx.Fail(
				http.StatusConflict, "email_taken", "ya existe una cuenta con ese correo")
		}
		return Session{}, err
	}

	return s.newSession(ctx, user)
}

// Login verifies a password and issues a session.
func (s *Service) Login(ctx context.Context, email, password string) (Session, error) {
	user, err := s.repo.UserByEmail(ctx, normalizeEmail(email))
	if err != nil {
		if errors.Is(err, ErrNoRows) {
			// Spend the time anyway. Returning immediately for an unknown email
			// turns response timing into a list of who has an account here.
			_, _ = HashPassword(password)
			return Session{}, errInvalidCredentials
		}
		return Session{}, err
	}

	ok, err := VerifyPassword(password, user.passwordHash)
	if err != nil || !ok {
		return Session{}, errInvalidCredentials
	}

	return s.newSession(ctx, user)
}

// Refresh rotates a refresh token for a new session.
func (s *Service) Refresh(ctx context.Context, refreshToken string) (Session, error) {
	userID, err := s.repo.ConsumeRefreshToken(ctx, HashRefreshToken(refreshToken))
	if err != nil {
		if errors.Is(err, ErrNoRows) {
			return Session{}, httpx.Fail(
				http.StatusUnauthorized, "invalid_refresh", "la sesión expiró, vuelve a entrar")
		}
		return Session{}, err
	}

	user, err := s.repo.UserByID(ctx, userID)
	if err != nil {
		return Session{}, err
	}
	return s.newSession(ctx, user)
}

// ChangePassword swaps the password after checking the current one.
//
// Every other session is revoked afterwards. A password change is usually a
// response to a suspected leak, so leaving other sessions alive would defeat
// the point.
func (s *Service) ChangePassword(
	ctx context.Context,
	userID uuid.UUID,
	currentPassword, newPassword string,
) error {
	user, err := s.repo.UserByID(ctx, userID)
	if err != nil {
		return err
	}

	ok, err := VerifyPassword(currentPassword, user.passwordHash)
	if err != nil || !ok {
		return httpx.Fail(
			http.StatusUnauthorized, "invalid_credentials", "la contraseña actual no coincide")
	}

	if len([]rune(newPassword)) < 8 {
		return httpx.Fail(
			http.StatusBadRequest, "weak_password", "la contraseña necesita al menos 8 caracteres")
	}

	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	if err := s.repo.UpdatePassword(ctx, userID, hash); err != nil {
		return err
	}
	return s.repo.RevokeAllForUser(ctx, userID)
}

// Logout ends every session for the user.
func (s *Service) Logout(ctx context.Context, userID uuid.UUID) error {
	return s.repo.RevokeAllForUser(ctx, userID)
}

// Me returns the caller's account plus their organization.
func (s *Service) Me(ctx context.Context, userID uuid.UUID) (User, string, error) {
	user, err := s.repo.UserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrNoRows) {
			return User{}, "", httpx.ErrNotFound
		}
		return User{}, "", err
	}
	orgID, err := s.repo.ActiveOrgID(ctx, userID)
	if err != nil && !errors.Is(err, ErrNoRows) {
		return User{}, "", err
	}
	if orgID == uuid.Nil {
		return user, "", nil
	}
	return user, orgID.String(), nil
}

func (s *Service) newSession(ctx context.Context, user User) (Session, error) {
	orgID, err := s.repo.ActiveOrgID(ctx, user.ID)
	if err != nil && !errors.Is(err, ErrNoRows) {
		return Session{}, err
	}

	org := ""
	if orgID != uuid.Nil {
		org = orgID.String()
	}

	access, err := s.tokens.IssueAccess(user.ID, org)
	if err != nil {
		return Session{}, err
	}

	refresh, hash, err := NewRefreshToken()
	if err != nil {
		return Session{}, err
	}
	expiry := time.Now().Add(s.tokens.RefreshTTL())
	if err := s.repo.StoreRefreshToken(ctx, user.ID, hash, expiry); err != nil {
		return Session{}, err
	}

	return Session{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresIn:    int(s.tokens.AccessTTL().Seconds()),
		TokenType:    "Bearer",
		User:         user,
		OrgID:        org,
	}, nil
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func validateCredentials(email, password string) error {
	if !strings.Contains(email, "@") || len(email) < 5 {
		return httpx.Fail(http.StatusBadRequest, "invalid_email", "correo inválido")
	}
	// Length is the only rule worth enforcing. Composition rules push people
	// toward predictable substitutions without adding real entropy.
	if len([]rune(password)) < 8 {
		return httpx.Fail(
			http.StatusBadRequest, "weak_password", "la contraseña necesita al menos 8 caracteres")
	}
	return nil
}
