// Command import-supabase loads a Supabase export into this service's database.
//
// It reads the JSON files produced by querying the old REST API, one per table,
// and rewrites every identifier: rows land under one target organization and
// one target user, because the old auth provider's user ids mean nothing here.
//
//	go run ./cmd/import-supabase -dir ./export -email operador@iglesia.test -org "Casa Vida"
//
// Running it twice is safe. Rows are inserted with their original primary keys
// and conflicts are skipped, so a partial run can simply be repeated.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/steven230500/introduce-api/internal/db"
)

func main() {
	dir := flag.String("dir", "./export", "directory holding the exported JSON files")
	email := flag.String("email", "", "email of the account that will own the imported rows")
	orgName := flag.String("org", "", "name of the organization to import into")
	flag.Parse()

	if *email == "" || *orgName == "" {
		log.Fatal("both -email and -org are required")
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	ctx := context.Background()
	pool, err := db.Connect(ctx, databaseURL)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer pool.Close()

	var userID, orgID uuid.UUID
	if err := pool.QueryRow(ctx, `select id from users where email = $1`, *email).Scan(&userID); err != nil {
		log.Fatalf("no account for %s: %v (register it first)", *email, err)
	}
	if err := pool.QueryRow(ctx, `
		select o.id from organizations o
		join organization_members m on m.org_id = o.id
		where o.name = $1 and m.user_id = $2`, *orgName, userID).Scan(&orgID); err != nil {
		log.Fatalf("no organization %q for that account: %v", *orgName, err)
	}

	log.Printf("importing into org %s as %s", orgID, userID)

	songs := readTable(*dir, "songs.json")
	verses := readTable(*dir, "verses.json")
	tmpl := readTable(*dir, "templates.json")
	cols := readTable(*dir, "collections.json")
	items := readTable(*dir, "collection_items.json")
	media := readTable(*dir, "media_items.json")

	counts := map[string]int{}

	for _, s := range songs {
		tag, err := pool.Exec(ctx, `
			insert into songs (id, org_id, created_by, title, author, copyright, ccli_number, language, tags)
			values ($1, $2, $3, $4, $5, $6, $7, coalesce($8, 'es'), coalesce($9, '{}'::text[]))
			on conflict (id) do nothing`,
			str(s, "id"), orgID, userID, s["title"], s["author"], s["copyright"],
			s["ccli_number"], s["language"], toStringSlice(s["tags"]))
		must(err, "song")
		counts["songs"] += int(tag.RowsAffected())
	}

	for _, v := range verses {
		tag, err := pool.Exec(ctx, `
			insert into verses (id, song_id, type, verse_order, content, chords)
			values ($1, $2, $3, $4, $5, $6)
			on conflict (id) do nothing`,
			str(v, "id"), str(v, "song_id"), v["type"], toInt(v["verse_order"]),
			v["content"], v["chords"])
		must(err, "verse")
		counts["verses"] += int(tag.RowsAffected())
	}

	for _, t := range tmpl {
		config := t["config"]
		if config == nil {
			config = map[string]any{}
		}
		raw, _ := json.Marshal(config)
		tag, err := pool.Exec(ctx, `
			insert into templates (id, org_id, created_by, name, config)
			values ($1, $2, $3, $4, $5)
			on conflict (id) do nothing`,
			str(t, "id"), orgID, userID, t["name"], raw)
		must(err, "template")
		counts["templates"] += int(tag.RowsAffected())
	}

	for _, c := range cols {
		tag, err := pool.Exec(ctx, `
			insert into collections (id, org_id, created_by, name, service_date, notes, template_id, bg_audio_path)
			values ($1, $2, $3, $4, $5, $6, $7, $8)
			on conflict (id) do nothing`,
			str(c, "id"), orgID, userID, c["name"], toDate(c["service_date"]),
			c["notes"], c["template_id"], c["bg_audio_path"])
		must(err, "collection")
		counts["collections"] += int(tag.RowsAffected())
	}

	for _, i := range items {
		var content any
		if i["content_json"] != nil {
			raw, _ := json.Marshal(i["content_json"])
			content = raw
		}
		itemType, _ := i["item_type"].(string)
		if itemType == "" {
			itemType = "song"
		}
		tag, err := pool.Exec(ctx, `
			insert into collection_items
			    (id, collection_id, song_id, template_id, item_order, item_type,
			     content_json, notes, auto_advance_secs)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			on conflict (id) do nothing`,
			str(i, "id"), str(i, "collection_id"), nullableUUID(i["song_id"]),
			i["template_id"], toInt(i["item_order"]), itemType, content,
			i["notes"], toIntPtr(i["auto_advance_secs"]))
		must(err, "collection item")
		counts["collection_items"] += int(tag.RowsAffected())
	}

	for _, m := range media {
		tag, err := pool.Exec(ctx, `
			insert into media_items (id, org_id, created_by, name, url, storage_path, media_type, size_bytes)
			values ($1, $2, $3, $4, $5, $6, $7, $8)
			on conflict (id) do nothing`,
			str(m, "id"), orgID, userID, m["name"], m["url"], m["storage_path"],
			m["media_type"], toIntPtr(m["size_bytes"]))
		must(err, "media item")
		counts["media_items"] += int(tag.RowsAffected())
	}

	for _, table := range []string{"songs", "verses", "templates", "collections", "collection_items", "media_items"} {
		fmt.Printf("%-18s %d imported\n", table, counts[table])
	}
}

func readTable(dir, name string) []map[string]any {
	path := filepath.Join(dir, name)
	body, err := os.ReadFile(path)
	if err != nil {
		log.Printf("skipping %s: %v", name, err)
		return nil
	}
	var rows []map[string]any
	if err := json.Unmarshal(body, &rows); err != nil {
		log.Fatalf("parse %s: %v", name, err)
	}
	return rows
}

func must(err error, what string) {
	if err != nil {
		log.Fatalf("insert %s: %v", what, err)
	}
}

func str(row map[string]any, key string) any {
	v, _ := row[key].(string)
	return v
}

func nullableUUID(v any) any {
	s, ok := v.(string)
	if !ok || s == "" {
		return nil
	}
	return s
}

func toInt(v any) int {
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return 0
}

func toIntPtr(v any) any {
	if f, ok := v.(float64); ok {
		i := int64(f)
		return &i
	}
	return nil
}

func toDate(v any) any {
	s, ok := v.(string)
	if !ok || s == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02", s[:min(10, len(s))])
	if err != nil {
		return nil
	}
	return t
}

func toStringSlice(v any) any {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
