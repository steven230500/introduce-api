package songs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/steven230500/introduce-api/internal/auth"
	"github.com/steven230500/introduce-api/internal/db"
)

func TestALibraryComesOverInOnePiece(t *testing.T) {
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
	router := NewHandler(repo).Router()

	send := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/import", bytes.NewBufferString(body))
		req = req.WithContext(auth.WithIdentity(req.Context(), userID, orgID))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	rec := send(`{"songs": [
		{"title": "Grande es tu fidelidad", "author": "Thomas Chisholm", "copyright": "© 1923 Hope Publishing",
		 "ccli_number": "18723", "verses": [
			{"type": "verse", "verse_order": 0, "content": "Oh Dios eterno"},
			{"type": "chorus", "verse_order": 1, "content": "Grande es tu fidelidad"},
			{"type": "ending", "verse_order": 2, "content": "Una sección que esta biblioteca no conoce"}
		]},
		{"title": "  Cuán grande es Él  ", "verses": [{"type": "verse", "verse_order": 0, "content": "Señor mi Dios"}]}
	]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
	}
	var out importResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out.IDs) != 2 {
		t.Fatalf("ids = %v", out.IDs)
	}

	first, err := repo.Get(ctx, orgID, out.IDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if first.CCLINumber == nil || *first.CCLINumber != "18723" || len(first.Verses) != 3 {
		t.Fatalf("first song came back as %+v", first)
	}
	if first.Verses[1].Type != "chorus" || first.Verses[2].Type != "verse" {
		t.Fatalf("verse types = %s, %s", first.Verses[1].Type, first.Verses[2].Type)
	}
	second, _ := repo.Get(ctx, orgID, out.IDs[1])
	if second.Title != "Cuán grande es Él" {
		t.Fatalf("title not trimmed: %q", second.Title)
	}

	// A song with no title stops the request before anything is written, and
	// says which one it was.
	before := count(t, pool, orgID)
	rec = send(`{"songs": [{"title": "Buena", "verses": []}, {"title": " ", "verses": []}]}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "canción 2") {
		t.Fatalf("untitled: %d %s", rec.Code, rec.Body.String())
	}
	if count(t, pool, orgID) != before {
		t.Fatal("part of a refused import was written")
	}

	// More than one request's worth is refused; the app sends it in pieces.
	var many strings.Builder
	many.WriteString(`{"songs": [`)
	for i := 0; i <= maxImport; i++ {
		if i > 0 {
			many.WriteString(",")
		}
		fmt.Fprintf(&many, `{"title": "Canción %d", "verses": []}`, i)
	}
	many.WriteString(`]}`)
	if rec := send(many.String()); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("too many: %d", rec.Code)
	}
}

func count(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `select count(*) from songs where org_id = $1`, orgID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
