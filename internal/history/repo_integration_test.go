package history

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/steven230500/introduce-api/internal/db"
)

// Runs against a real database when TEST_DATABASE_URL is set, and skips
// otherwise. The promise this package makes - a resend counts once - lives in
// an ON CONFLICT clause, and only a database can check that.
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

func seedOrg(t *testing.T, pool *pgxpool.Pool) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	var userID, orgID uuid.UUID
	if err := pool.QueryRow(ctx,
		`insert into users (email, password_hash) values ($1, 'x') returning id`,
		uuid.NewString()+"@test.invalid").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`insert into organizations (name) values ('Iglesia de prueba') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	return orgID, userID
}

func TestSendingTheSameServiceTwiceCountsItOnce(t *testing.T) {
	pool := testPool(t)
	repo := NewRepo(pool)
	orgID, userID := seedOrg(t, pool)
	ctx := context.Background()

	start := time.Date(2026, 6, 28, 10, 0, 0, 0, time.UTC)
	author := "Un Corazón"
	events := []Event{
		{ID: uuid.New(), ItemType: "song", Title: "Nada es imposible", SongAuthor: &author,
			StartedAt: start, EndedAt: start.Add(4 * time.Minute)},
		{ID: uuid.New(), ItemType: "bible_verse", Title: "Génesis 1:1",
			StartedAt: start.Add(4 * time.Minute), EndedAt: start.Add(6 * time.Minute)},
	}

	first, err := repo.Record(ctx, orgID, userID, events)
	if err != nil {
		t.Fatal(err)
	}
	// The client could not tell whether the first send arrived, so it sends
	// again. This is the case the whole design exists for.
	second, err := repo.Record(ctx, orgID, userID, events)
	if err != nil {
		t.Fatal(err)
	}

	if first != 2 || second != 0 {
		t.Fatalf("stored %d then %d, want 2 then 0", first, second)
	}

	read, err := repo.Between(ctx, orgID, start.Add(-time.Hour), start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(read) != 2 {
		t.Fatalf("read back %d events, want 2", len(read))
	}
	if read[0].Title != "Nada es imposible" || read[1].Title != "Génesis 1:1" {
		t.Fatalf("not in the order they happened: %q, %q", read[0].Title, read[1].Title)
	}
	if read[0].SongAuthor == nil || *read[0].SongAuthor != author {
		t.Fatal("the author did not survive the round trip")
	}
}

func TestOneChurchNeverReadsAnothersHistory(t *testing.T) {
	pool := testPool(t)
	repo := NewRepo(pool)
	ctx := context.Background()

	ours, _ := seedOrg(t, pool)
	theirs, other := seedOrg(t, pool)

	start := time.Date(2026, 7, 5, 10, 0, 0, 0, time.UTC)
	if _, err := repo.Record(ctx, theirs, other, []Event{
		{ID: uuid.New(), ItemType: "song", Title: "Suya", StartedAt: start, EndedAt: start},
	}); err != nil {
		t.Fatal(err)
	}

	read, err := repo.Between(ctx, ours, start.Add(-time.Hour), start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(read) != 0 {
		t.Fatalf("read %d events belonging to another church", len(read))
	}
}

func TestTheRangeIsHalfOpen(t *testing.T) {
	// A report for June and a report for July must not both count the song
	// that started at midnight on the first of July.
	pool := testPool(t)
	repo := NewRepo(pool)
	orgID, userID := seedOrg(t, pool)
	ctx := context.Background()

	july := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	if _, err := repo.Record(ctx, orgID, userID, []Event{
		{ID: uuid.New(), ItemType: "song", Title: "Medianoche", StartedAt: july, EndedAt: july},
	}); err != nil {
		t.Fatal(err)
	}

	june, _ := repo.Between(ctx, orgID, july.AddDate(0, -1, 0), july)
	julyRead, _ := repo.Between(ctx, orgID, july, july.AddDate(0, 1, 0))

	if len(june) != 0 || len(julyRead) != 1 {
		t.Fatalf("june %d, july %d: the boundary event was counted in the wrong month", len(june), len(julyRead))
	}
}
