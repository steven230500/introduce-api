package collections

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/steven230500/introduce-api/internal/db"
)

func TestAnItemKeepsHowLongItIsMeantToTake(t *testing.T) {
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

	var orgID, collectionID uuid.UUID
	if err := pool.QueryRow(ctx, `insert into organizations (name) values ('Iglesia') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into collections (org_id, name) values ($1, 'Domingo') returning id`,
		orgID).Scan(&collectionID); err != nil {
		t.Fatal(err)
	}
	repo := NewRepo(pool)

	sermon := 35 * 60
	if err := repo.AddItems(ctx, orgID, collectionID, []ItemInput{
		{ItemType: "sermon", PlannedSecs: &sermon},
		{ItemType: "free_slide"},
	}); err != nil {
		t.Fatal(err)
	}

	items := func() []Item {
		services, err := repo.List(ctx, orgID)
		if err != nil {
			t.Fatal(err)
		}
		return services[0].Items
	}
	got := items()
	if got[0].PlannedSecs == nil || *got[0].PlannedSecs != sermon {
		t.Fatalf("sermon plan = %v, want %d", got[0].PlannedSecs, sermon)
	}
	if got[1].PlannedSecs != nil {
		t.Fatalf("an item with no plan came back with %d", *got[1].PlannedSecs)
	}

	// Set from a rehearsal, as the JSON number the app sends.
	if err := repo.UpdateItem(ctx, orgID, got[1].ID, map[string]any{"planned_secs": float64(272)}); err != nil {
		t.Fatal(err)
	}
	if p := items()[1].PlannedSecs; p == nil || *p != 272 {
		t.Fatalf("after update = %v, want 272", p)
	}

	// A zero from a rehearsal that never really started clears the plan rather
	// than failing the save on the check constraint.
	if err := repo.UpdateItem(ctx, orgID, got[1].ID, map[string]any{"planned_secs": float64(0)}); err != nil {
		t.Fatal(err)
	}
	if p := items()[1].PlannedSecs; p != nil {
		t.Fatalf("a zero plan was stored as %d", *p)
	}

	// And null clears it on purpose.
	if err := repo.UpdateItem(ctx, orgID, got[0].ID, map[string]any{"planned_secs": nil}); err != nil {
		t.Fatal(err)
	}
	if p := items()[0].PlannedSecs; p != nil {
		t.Fatalf("a cleared plan came back as %d", *p)
	}
}
