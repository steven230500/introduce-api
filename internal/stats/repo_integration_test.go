package stats

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/steven230500/introduce-api/internal/db"
)

// Runs against a real database when TEST_DATABASE_URL is set, and skips
// otherwise. The dashboard's numbers are SQL, and only a database can check
// that "copies" means computers and not launches.
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
	// Each test reads totals over the whole table, so it starts from none.
	if _, err := pool.Exec(ctx, `delete from app_events`); err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestACopyOpenedManyTimesIsOneCopy(t *testing.T) {
	pool := testPool(t)
	repo := NewRepo(pool)
	ctx := context.Background()

	laptop, desk := uuid.New(), uuid.New()
	for _, e := range []Event{
		{Name: "app_open", Platform: "macos", AppVersion: "1.0.2", Country: "CO", InstallID: &laptop},
		{Name: "app_open", Platform: "macos", AppVersion: "1.0.2", Country: "CO", InstallID: &laptop},
		{Name: "app_open", Platform: "macos", AppVersion: "1.0.2", Country: "CO", InstallID: &laptop},
		{Name: "app_open", Platform: "windows", AppVersion: "1.0.2", Country: "MX", InstallID: &desk},
		{Name: "download", Platform: "macos", Country: "CO"},
		{Name: "download", Platform: "windows", Country: "PE"},
	} {
		if err := repo.Record(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	s, err := repo.Summarize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s.Copies.Week != 2 || s.Copies.Total != 2 {
		t.Fatalf("copies %+v, want 2", s.Copies)
	}
	if s.Downloads.Total != 2 {
		t.Fatalf("downloads %+v, want 2", s.Downloads)
	}
	if len(s.CopiesByCountry) != 2 || s.CopiesByCountry[0].Total != 1 {
		t.Fatalf("copies by country %+v", s.CopiesByCountry)
	}
}

func TestAChurchWithNoRecordedCountryTakesItFromItsCopies(t *testing.T) {
	pool := testPool(t)
	repo := NewRepo(pool)
	ctx := context.Background()

	var orgID uuid.UUID
	name := "Iglesia " + uuid.NewString()
	if err := pool.QueryRow(ctx,
		`insert into organizations (name) values ($1) returning id`, name).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `delete from organizations where id = $1`, orgID) })

	install := uuid.New()
	if err := repo.Record(ctx, Event{
		Name: "app_open", Platform: "macos", AppVersion: "1.0.2", Country: "EC", InstallID: &install, OrgID: &orgID,
	}); err != nil {
		t.Fatal(err)
	}

	s, err := repo.Summarize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range s.Churches {
		if c.Name == name {
			if c.Country != "EC" || c.LastOpened == nil {
				t.Fatalf("church %+v", c)
			}
			return
		}
	}
	t.Fatalf("church %q not listed", name)
}
