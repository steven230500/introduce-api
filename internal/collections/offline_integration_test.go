package collections

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/steven230500/introduce-api/internal/db"
)

// A service planned in a building with no internet reaches the server later,
// as a queue: the service, then its items, then whatever was changed on them.
// Every later request names the ids the app chose, so the server has to keep
// them, and a queue replayed because a response was lost must not double up.
func TestAServiceMadeOfflineKeepsTheIdsTheAppChose(t *testing.T) {
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

	org := func(name string) uuid.UUID {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `insert into organizations (name) values ($1) returning id`,
			name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	church, other := org("Iglesia"), org("Otra iglesia")
	var user uuid.UUID
	if err := pool.QueryRow(ctx, `insert into users (email, password_hash) values ($1, 'x') returning id`,
		uuid.NewString()+"@test.invalid").Scan(&user); err != nil {
		t.Fatal(err)
	}
	repo := NewRepo(pool)

	serviceID := uuid.New()
	date := "2026-09-20"
	in := CollectionInput{ID: &serviceID, Name: "Domingo", ServiceDate: &date}

	made, created, err := repo.Create(ctx, church, user, in)
	if err != nil {
		t.Fatal(err)
	}
	if made.ID != serviceID || !created {
		t.Fatalf("created %v (new: %v), want %v", made.ID, created, serviceID)
	}

	again, created, err := repo.Create(ctx, church, user, in)
	if err != nil {
		t.Fatalf("the same service sent twice failed: %v", err)
	}
	if again.ID != serviceID || created {
		t.Fatalf("a repeat came back as %v (new: %v)", again.ID, created)
	}

	if _, _, err := repo.Create(ctx, other, user, in); !errors.Is(err, ErrIDTaken) {
		t.Fatalf("another church reusing the id got %v, want ErrIDTaken", err)
	}

	first, second := uuid.New(), uuid.New()
	items := []ItemInput{
		{ID: &first, ItemType: "free_slide"},
		{ID: &second, ItemType: "sermon"},
	}
	if err := repo.AddItems(ctx, church, serviceID, items); err != nil {
		t.Fatal(err)
	}
	// The whole add replayed, plus one item that was not in it before.
	third := uuid.New()
	if err := repo.AddItems(ctx, church, serviceID, append(items, ItemInput{ID: &third})); err != nil {
		t.Fatalf("a replayed add failed: %v", err)
	}

	services, err := repo.List(ctx, church)
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 {
		t.Fatalf("%d services, want 1", len(services))
	}
	got := services[0].Items
	want := []uuid.UUID{first, second, third}
	if len(got) != len(want) {
		t.Fatalf("%d items, want %d", len(got), len(want))
	}
	for i, item := range got {
		if item.ID != want[i] || item.ItemOrder != i {
			t.Fatalf("item %d = %v at %d, want %v at %d", i, item.ID, item.ItemOrder, want[i], i)
		}
	}

	// Deleted, then put back by undo: the same id comes back to life.
	if err := repo.DeleteItem(ctx, church, second); err != nil {
		t.Fatal(err)
	}
	if err := repo.AddItems(ctx, church, serviceID, []ItemInput{{ID: &second, ItemType: "sermon"}}); err != nil {
		t.Fatalf("restoring a deleted id failed: %v", err)
	}
	services, err = repo.List(ctx, church)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(services[0].Items); n != 3 {
		t.Fatalf("%d items after restore, want 3", n)
	}

	// Someone else's collection is still out of reach, id or not.
	if err := repo.AddItems(ctx, other, serviceID, []ItemInput{{ItemType: "free_slide"}}); err == nil {
		t.Fatal("another church added an item to this service")
	}
}
