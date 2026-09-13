package presentation

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/steven230500/introduce-api/internal/db"
)

func TestTheWaitingScreenSurvivesBeingStored(t *testing.T) {
	// A window that connects mid-service reads the stored state first. If the
	// waiting screen did not come back with it, a projector opened while the
	// pre-service loop is up would show black until the operator touched
	// something.
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

	var userID uuid.UUID
	if err := pool.QueryRow(ctx, `insert into users (email, password_hash) values ($1, 'x') returning id`,
		uuid.NewString()+"@test.invalid").Scan(&userID); err != nil {
		t.Fatal(err)
	}

	repo := NewRepo(pool)
	waiting := json.RawMessage(`{"active":true,"scene":"aurora","title":"Bienvenidos","clock":true}`)
	if _, err := repo.Upsert(ctx, userID, State{IsLive: true, Waiting: waiting}); err != nil {
		t.Fatal(err)
	}

	got, err := repo.Get(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(got.Waiting, &decoded); err != nil {
		t.Fatalf("waiting came back unreadable: %v (%s)", err, got.Waiting)
	}
	if decoded["scene"] != "aurora" || decoded["title"] != "Bienvenidos" || decoded["active"] != true {
		t.Fatalf("waiting came back as %s", got.Waiting)
	}

	// Taking the waiting screen away must actually take it away.
	if _, err := repo.Upsert(ctx, userID, State{IsLive: true}); err != nil {
		t.Fatal(err)
	}
	cleared, err := repo.Get(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cleared.Waiting) != 0 {
		t.Fatalf("an absent waiting screen came back as %s", cleared.Waiting)
	}

	// And it is left out of the JSON a window receives, rather than sent as null.
	payload, _ := json.Marshal(cleared)
	var fields map[string]any
	_ = json.Unmarshal(payload, &fields)
	if _, present := fields["waiting"]; present {
		t.Fatalf("an absent waiting screen was sent to the windows: %s", payload)
	}
}

func TestTheTimingOfTheServiceSurvivesBeingStored(t *testing.T) {
	// A stage display opened on another machine mid-sermon reads the stored
	// state first. Without the timing in it, the preacher's clock would start
	// from zero at the moment the screen was plugged in.
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

	var userID uuid.UUID
	if err := pool.QueryRow(ctx, `insert into users (email, password_hash) values ($1, 'x') returning id`,
		uuid.NewString()+"@test.invalid").Scan(&userID); err != nil {
		t.Fatal(err)
	}

	repo := NewRepo(pool)
	timing := json.RawMessage(`{"item_started_at":"2026-09-13T15:04:05Z","planned_secs":2100,"rehearsal":false}`)
	if _, err := repo.Upsert(ctx, userID, State{IsLive: true, Timing: timing}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(got.Timing, &decoded); err != nil {
		t.Fatalf("timing came back unreadable: %v (%s)", err, got.Timing)
	}
	if decoded["planned_secs"] != float64(2100) || decoded["item_started_at"] != "2026-09-13T15:04:05Z" {
		t.Fatalf("timing came back as %s", got.Timing)
	}
}
