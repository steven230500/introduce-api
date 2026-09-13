package collections

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/steven230500/introduce-api/internal/db"
)

func TestASongInAServiceCarriesItsLicenceDetails(t *testing.T) {
	// The projection record copies these at the moment a song goes on screen.
	// If the service's copy of the song leaves them out, every licence report
	// comes out as a list of titles with no numbers, which nobody can file.
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

	var orgID, songID, collectionID uuid.UUID
	if err := pool.QueryRow(ctx, `insert into organizations (name) values ('Iglesia') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		insert into songs (org_id, title, author, copyright, ccli_number)
		values ($1, 'Grande es tu fidelidad', 'Thomas Chisholm', '1923 Hope Publishing', '18723')
		returning id`, orgID).Scan(&songID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into collections (org_id, name) values ($1, 'Domingo') returning id`,
		orgID).Scan(&collectionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		insert into collection_items (collection_id, item_type, item_order, song_id)
		values ($1, 'song', 0, $2)`, collectionID, songID); err != nil {
		t.Fatal(err)
	}

	services, err := NewRepo(pool).List(ctx, orgID)
	if err != nil {
		t.Fatal(err)
	}
	song := services[0].Items[0].Song
	if song == nil {
		t.Fatal("the item came back without its song")
	}
	if song.CCLINumber == nil || *song.CCLINumber != "18723" {
		t.Fatalf("licence number = %v, want 18723", song.CCLINumber)
	}
	if song.Copyright == nil || *song.Copyright != "1923 Hope Publishing" {
		t.Fatalf("copyright = %v", song.Copyright)
	}
}
