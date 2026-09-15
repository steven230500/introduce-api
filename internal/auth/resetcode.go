package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/steven230500/introduce-api/internal/httpx"
)

// Letters and digits that cannot be misread when a code is read aloud or
// copied from a phone: no 0/O, 1/I/L.
const resetAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

const (
	resetCodeLength = 8
	// Long enough for a pastor to reach the volunteer after the service.
	ResetCodeTTL = 24 * time.Hour
	// Wrong guesses allowed before the code is thrown away. With 31^8 codes,
	// five guesses leave no practical chance of stumbling on it.
	resetMaxAttempts = 5
)

// NewResetCode returns a code as it is shown (ABCD-EFGH) and the hash to store.
func NewResetCode() (string, []byte, error) {
	var b strings.Builder
	max := big.NewInt(int64(len(resetAlphabet)))
	for i := 0; i < resetCodeLength; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", nil, err
		}
		if i == resetCodeLength/2 {
			b.WriteByte('-')
		}
		b.WriteByte(resetAlphabet[n.Int64()])
	}
	code := b.String()
	return code, HashResetCode(code), nil
}

// HashResetCode hashes a code the way it was typed: case, spaces and the dash
// do not matter.
func HashResetCode(code string) []byte {
	normalized := strings.Map(func(r rune) rune {
		if r == '-' || r == ' ' {
			return -1
		}
		return r
	}, strings.ToUpper(strings.TrimSpace(code)))
	sum := sha256.Sum256([]byte(normalized))
	return sum[:]
}

// StoreResetCode saves a code for userID, replacing any code it had.
func (r *Repo) StoreResetCode(ctx context.Context, userID, createdBy uuid.UUID, hash []byte, expiresAt time.Time) error {
	_, err := r.pool.Exec(ctx, `
		insert into password_reset_codes (user_id, code_hash, created_by, expires_at, attempts)
		values ($1, $2, $3, $4, 0)
		on conflict (user_id) do update
		set code_hash = excluded.code_hash, created_by = excluded.created_by,
		    expires_at = excluded.expires_at, attempts = 0`,
		userID, hash, createdBy, expiresAt)
	return err
}

// ConsumeResetCode checks a code for userID. A right code is used up; a wrong
// one counts as an attempt, and the last allowed attempt throws the code away.
// Any failure is ErrNoRows, so the caller cannot tell "wrong" from "expired".
func (r *Repo) ConsumeResetCode(ctx context.Context, userID uuid.UUID, hash []byte) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var stored []byte
	var expiresAt time.Time
	var attempts int
	err = tx.QueryRow(ctx, `
		select code_hash, expires_at, attempts from password_reset_codes
		where user_id = $1 for update`, userID,
	).Scan(&stored, &expiresAt, &attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNoRows
	}
	if err != nil {
		return err
	}

	switch {
	case time.Now().After(expiresAt):
		_, err = tx.Exec(ctx, `delete from password_reset_codes where user_id = $1`, userID)
		if err == nil {
			err = tx.Commit(ctx)
		}
		if err != nil {
			return err
		}
		return ErrNoRows
	case subtle.ConstantTimeCompare(stored, hash) != 1:
		if attempts+1 >= resetMaxAttempts {
			_, err = tx.Exec(ctx, `delete from password_reset_codes where user_id = $1`, userID)
		} else {
			_, err = tx.Exec(ctx,
				`update password_reset_codes set attempts = attempts + 1 where user_id = $1`, userID)
		}
		if err == nil {
			err = tx.Commit(ctx)
		}
		if err != nil {
			return err
		}
		return ErrNoRows
	}

	if _, err := tx.Exec(ctx, `delete from password_reset_codes where user_id = $1`, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

var errInvalidResetCode = httpx.Fail(
	http.StatusBadRequest, "invalid_reset_code", "el código no es válido o ya venció")

// ResetPassword sets a new password with a code an administrator created.
// Every session the account had is ended, as with a normal password change.
func (s *Service) ResetPassword(ctx context.Context, email, code, newPassword string) error {
	if len([]rune(newPassword)) < 8 {
		return httpx.Fail(
			http.StatusBadRequest, "weak_password", "la contraseña necesita al menos 8 caracteres")
	}

	user, err := s.repo.UserByEmail(ctx, normalizeEmail(email))
	if errors.Is(err, ErrNoRows) {
		// Spend the time anyway, as Login does, so the answer's speed does not
		// say which emails have an account.
		_, _ = HashPassword(newPassword)
		return errInvalidResetCode
	}
	if err != nil {
		return err
	}

	if err := s.repo.ConsumeResetCode(ctx, user.ID, HashResetCode(code)); err != nil {
		if errors.Is(err, ErrNoRows) {
			return errInvalidResetCode
		}
		return err
	}

	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	if err := s.repo.UpdatePassword(ctx, user.ID, hash); err != nil {
		return err
	}
	return s.repo.RevokeAllForUser(ctx, user.ID)
}
