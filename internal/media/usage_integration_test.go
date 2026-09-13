package media

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/steven230500/introduce-api/internal/db"
)

func TestUsageAddsUpWhatTheChurchUploaded(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	var userID, orgID uuid.UUID
	if err := pool.QueryRow(ctx, `insert into users (email, password_hash) values ($1, 'x') returning id`,
		uuid.NewString()+"@test.invalid").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into organizations (name) values ('Iglesia') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}

	repo := NewRepo(pool)

	// A church that has uploaded nothing is on the free plan with nothing used,
	// not an error: this runs before every first upload.
	used, plan, err := repo.Usage(ctx, orgID)
	if err != nil {
		t.Fatal(err)
	}
	if used != 0 || plan != "free" {
		t.Fatalf("empty church: used %d, plan %q", used, plan)
	}

	for _, size := range []int64{3 << 20, 5 << 20} {
		s := size
		if _, err := repo.Create(ctx, orgID, userID, Item{
			Name: "foto", URL: "http://x", StoragePath: "images/" + uuid.NewString(),
			MediaType: "image", SizeBytes: &s,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `update organizations set plan = 'iglesia' where id = $1`, orgID); err != nil {
		t.Fatal(err)
	}

	used, plan, err = repo.Usage(ctx, orgID)
	if err != nil {
		t.Fatal(err)
	}
	if used != 8<<20 {
		t.Fatalf("used %d bytes, want %d", used, int64(8<<20))
	}
	if plan != "iglesia" {
		t.Fatalf("plan %q, want iglesia", plan)
	}
}
