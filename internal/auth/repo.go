package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// User is an account that can sign in.
type User struct {
	ID           uuid.UUID `json:"id"`
	Email        string    `json:"email"`
	DisplayName  *string   `json:"display_name"`
	passwordHash string
}

var ErrNoRows = errors.New("not found")

type Repo struct{ pool *pgxpool.Pool }

func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

func (r *Repo) CreateUser(ctx context.Context, email, passwordHash string, displayName *string) (User, error) {
	var u User
	err := r.pool.QueryRow(ctx, `
		insert into users (email, password_hash, display_name)
		values ($1, $2, $3)
		returning id, email, display_name, password_hash`,
		email, passwordHash, displayName,
	).Scan(&u.ID, &u.Email, &u.DisplayName, &u.passwordHash)
	return u, err
}

func (r *Repo) UserByEmail(ctx context.Context, email string) (User, error) {
	var u User
	err := r.pool.QueryRow(ctx, `
		select id, email, display_name, password_hash from users where email = $1`,
		email,
	).Scan(&u.ID, &u.Email, &u.DisplayName, &u.passwordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNoRows
	}
	return u, err
}

func (r *Repo) UserByID(ctx context.Context, id uuid.UUID) (User, error) {
	var u User
	err := r.pool.QueryRow(ctx, `
		select id, email, display_name, password_hash from users where id = $1`,
		id,
	).Scan(&u.ID, &u.Email, &u.DisplayName, &u.passwordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNoRows
	}
	return u, err
}

// ActiveOrgID returns the organization the user belongs to, if any.
func (r *Repo) ActiveOrgID(ctx context.Context, userID uuid.UUID) (uuid.UUID, error) {
	var orgID uuid.UUID
	err := r.pool.QueryRow(ctx, `
		select org_id from organization_members
		where user_id = $1 and status = 'active'
		order by joined_at
		limit 1`, userID,
	).Scan(&orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNoRows
	}
	return orgID, err
}

func (r *Repo) StoreRefreshToken(ctx context.Context, userID uuid.UUID, hash []byte, expiresAt time.Time) error {
	_, err := r.pool.Exec(ctx, `
		insert into refresh_tokens (user_id, token_hash, expires_at)
		values ($1, $2, $3)`, userID, hash, expiresAt)
	return err
}

// ConsumeRefreshToken revokes a refresh token and returns its owner.
//
// Revoking on use is what makes rotation meaningful: a stolen token works at
// most once, and the theft shows up as a failed refresh for the real user.
func (r *Repo) ConsumeRefreshToken(ctx context.Context, hash []byte) (uuid.UUID, error) {
	var userID uuid.UUID
	err := r.pool.QueryRow(ctx, `
		update refresh_tokens
		set revoked_at = now()
		where token_hash = $1
		  and revoked_at is null
		  and expires_at > now()
		returning user_id`, hash,
	).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNoRows
	}
	return userID, err
}

// UpdatePassword replaces the stored hash.
func (r *Repo) UpdatePassword(ctx context.Context, userID uuid.UUID, hash string) error {
	tag, err := r.pool.Exec(ctx,
		`update users set password_hash = $2 where id = $1`, userID, hash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNoRows
	}
	return nil
}

// RevokeAllForUser ends every session, used on sign-out.
func (r *Repo) RevokeAllForUser(ctx context.Context, userID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		update refresh_tokens set revoked_at = now()
		where user_id = $1 and revoked_at is null`, userID)
	return err
}
