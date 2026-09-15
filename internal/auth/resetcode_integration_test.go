package auth

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/steven230500/introduce-api/internal/db"
	"github.com/steven230500/introduce-api/internal/httpx"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return pool
}

func newAccount(t *testing.T, svc *Service) (string, Session) {
	t.Helper()
	email := uuid.NewString() + "@test.invalid"
	session, err := svc.Register(context.Background(), email, "la-clave-vieja", nil)
	if err != nil {
		t.Fatal(err)
	}
	return email, session
}

func codeError(t *testing.T, err error) string {
	t.Helper()
	var apiErr *httpx.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("not an API error: %v", err)
	}
	return apiErr.Code
}

func TestACodeFromAnAdministratorSetsANewPasswordOnce(t *testing.T) {
	pool := testPool(t)
	repo := NewRepo(pool)
	svc := NewService(repo, NewTokenIssuer([]byte(strings.Repeat("k", 40)), time.Minute, time.Hour))
	ctx := context.Background()
	email, session := newAccount(t, svc)

	code, hash, err := NewResetCode()
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.StoreResetCode(ctx, session.User.ID, session.User.ID, hash, time.Now().Add(ResetCodeTTL)); err != nil {
		t.Fatal(err)
	}

	// Typed as a person would copy it from a message.
	typed := " " + strings.ToLower(strings.ReplaceAll(code, "-", "")) + " "
	if err := svc.ResetPassword(ctx, strings.ToUpper(email), typed, "la-clave-nueva"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if _, err := svc.Login(ctx, email, "la-clave-nueva"); err != nil {
		t.Fatalf("new password does not sign in: %v", err)
	}
	if _, err := svc.Login(ctx, email, "la-clave-vieja"); err == nil {
		t.Fatal("old password still signs in")
	}
	if _, err := svc.Refresh(ctx, session.RefreshToken); err == nil {
		t.Fatal("a session from before the reset survived it")
	}
	if err := svc.ResetPassword(ctx, email, code, "otra-clave-mas"); codeError(t, err) != "invalid_reset_code" {
		t.Fatalf("the code worked twice: %v", err)
	}
}

func TestFiveWrongGuessesThrowTheCodeAway(t *testing.T) {
	pool := testPool(t)
	repo := NewRepo(pool)
	svc := NewService(repo, NewTokenIssuer([]byte(strings.Repeat("k", 40)), time.Minute, time.Hour))
	ctx := context.Background()
	email, session := newAccount(t, svc)

	code, hash, _ := NewResetCode()
	if err := repo.StoreResetCode(ctx, session.User.ID, session.User.ID, hash, time.Now().Add(ResetCodeTTL)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := svc.ResetPassword(ctx, email, "AAAA-AAAA", "la-clave-nueva"); codeError(t, err) != "invalid_reset_code" {
			t.Fatalf("guess %d: %v", i, err)
		}
	}
	if err := svc.ResetPassword(ctx, email, code, "la-clave-nueva"); codeError(t, err) != "invalid_reset_code" {
		t.Fatalf("the right code still worked after five wrong guesses: %v", err)
	}
}

func TestAnExpiredCodeDoesNotWork(t *testing.T) {
	pool := testPool(t)
	repo := NewRepo(pool)
	svc := NewService(repo, NewTokenIssuer([]byte(strings.Repeat("k", 40)), time.Minute, time.Hour))
	ctx := context.Background()
	email, session := newAccount(t, svc)

	code, hash, _ := NewResetCode()
	if err := repo.StoreResetCode(ctx, session.User.ID, session.User.ID, hash, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := svc.ResetPassword(ctx, email, code, "la-clave-nueva"); codeError(t, err) != "invalid_reset_code" {
		t.Fatalf("expired code: %v", err)
	}
}

func TestAnUnknownEmailAndAShortPasswordAreTurnedAway(t *testing.T) {
	pool := testPool(t)
	svc := NewService(NewRepo(pool), NewTokenIssuer([]byte(strings.Repeat("k", 40)), time.Minute, time.Hour))
	ctx := context.Background()

	if err := svc.ResetPassword(ctx, "nadie@test.invalid", "ABCD-EFGH", "la-clave-nueva"); codeError(t, err) != "invalid_reset_code" {
		t.Fatalf("unknown email: %v", err)
	}
	if err := svc.ResetPassword(ctx, "nadie@test.invalid", "ABCD-EFGH", "corta"); codeError(t, err) != "weak_password" {
		t.Fatalf("short password: %v", err)
	}
}

func TestCodesAreEasyToReadAndHardToGuess(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		code, hash, err := NewResetCode()
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != 9 || code[4] != '-' || strings.ContainsAny(code, "0O1IL") {
			t.Fatalf("code %q", code)
		}
		if string(HashResetCode(strings.ToLower(code))) != string(hash) {
			t.Fatal("case changes the hash")
		}
		seen[code] = true
	}
	if len(seen) < 199 {
		t.Fatalf("only %d different codes in 200", len(seen))
	}
}
