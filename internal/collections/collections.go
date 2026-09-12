// Package collections owns service plans and the ordered items inside them.
package collections

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/steven230500/introduce-api/internal/auth"
	"github.com/steven230500/introduce-api/internal/httpx"
)

// The wire shape deliberately mirrors what the client already parses: items
// arrive under "collection_items" and a song under "songs". Keeping those keys
// means the desktop models and their tests did not have to change when the
// backend did.

type Verse struct {
	ID         uuid.UUID `json:"id"`
	SongID     uuid.UUID `json:"song_id"`
	Type       string    `json:"type"`
	VerseOrder int       `json:"verse_order"`
	Content    string    `json:"content"`
	Chords     *string   `json:"chords"`
}

type Song struct {
	ID       uuid.UUID `json:"id"`
	Title    string    `json:"title"`
	Author   *string   `json:"author"`
	Language string    `json:"language"`
	Tags     []string  `json:"tags"`
	Verses   []Verse   `json:"verses"`
}

type Item struct {
	ID              uuid.UUID       `json:"id"`
	CollectionID    uuid.UUID       `json:"collection_id"`
	ItemType        string          `json:"item_type"`
	ItemOrder       int             `json:"item_order"`
	TemplateID      *string         `json:"template_id"`
	ContentJSON     json.RawMessage `json:"content_json"`
	Notes           *string         `json:"notes"`
	AutoAdvanceSecs *int            `json:"auto_advance_secs"`
	Song            *Song           `json:"songs"`
}

type Collection struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	ServiceDate *string   `json:"service_date"`
	Notes       *string   `json:"notes"`
	TemplateID  *string   `json:"template_id"`
	BgAudioPath *string   `json:"bg_audio_path"`
	CreatedAt   time.Time `json:"created_at"`
	Items       []Item    `json:"collection_items"`
}

type Repo struct{ pool *pgxpool.Pool }

func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

// List returns every collection with its items and any songs they reference.
//
// Three queries rather than one join tree: a join would multiply each
// collection row by items and again by verses, and assembling that back into a
// tree costs more than the extra round trips.
func (r *Repo) List(ctx context.Context, orgID uuid.UUID) ([]Collection, error) {
	rows, err := r.pool.Query(ctx, `
		select id, name, service_date, notes, template_id, bg_audio_path, created_at
		from collections
		where org_id = $1
		order by service_date desc nulls last, created_at desc`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Collection{}
	index := map[uuid.UUID]int{}
	for rows.Next() {
		var c Collection
		var serviceDate *time.Time
		if err := rows.Scan(&c.ID, &c.Name, &serviceDate, &c.Notes,
			&c.TemplateID, &c.BgAudioPath, &c.CreatedAt); err != nil {
			return nil, err
		}
		if serviceDate != nil {
			formatted := serviceDate.Format("2006-01-02")
			c.ServiceDate = &formatted
		}
		c.Items = []Item{}
		index[c.ID] = len(out)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}

	ids := make([]uuid.UUID, 0, len(out))
	for id := range index {
		ids = append(ids, id)
	}

	itemRows, err := r.pool.Query(ctx, `
		select i.id, i.collection_id, i.item_type, i.item_order, i.template_id,
		       i.content_json, i.notes, i.auto_advance_secs,
		       s.id, s.title, s.author, s.language, s.tags
		from collection_items i
		left join songs s on s.id = i.song_id
		where i.collection_id = any($1)
		order by i.collection_id, i.item_order`, ids)
	if err != nil {
		return nil, err
	}
	defer itemRows.Close()

	songIndex := map[uuid.UUID][]*Song{}
	for itemRows.Next() {
		var it Item
		var songID *uuid.UUID
		var title, author, language *string
		var tags []string
		if err := itemRows.Scan(&it.ID, &it.CollectionID, &it.ItemType, &it.ItemOrder,
			&it.TemplateID, &it.ContentJSON, &it.Notes, &it.AutoAdvanceSecs,
			&songID, &title, &author, &language, &tags); err != nil {
			return nil, err
		}
		if songID != nil {
			it.Song = &Song{
				ID:       *songID,
				Title:    deref(title),
				Author:   author,
				Language: deref(language),
				Tags:     tags,
				Verses:   []Verse{},
			}
		}
		ci, ok := index[it.CollectionID]
		if !ok {
			continue
		}
		out[ci].Items = append(out[ci].Items, it)
		if it.Song != nil {
			last := &out[ci].Items[len(out[ci].Items)-1]
			songIndex[*songID] = append(songIndex[*songID], last.Song)
		}
	}
	if err := itemRows.Err(); err != nil {
		return nil, err
	}
	if len(songIndex) == 0 {
		return out, nil
	}

	songIDs := make([]uuid.UUID, 0, len(songIndex))
	for id := range songIndex {
		songIDs = append(songIDs, id)
	}

	verseRows, err := r.pool.Query(ctx, `
		select id, song_id, type, verse_order, content, chords
		from verses where song_id = any($1)
		order by song_id, verse_order`, songIDs)
	if err != nil {
		return nil, err
	}
	defer verseRows.Close()

	for verseRows.Next() {
		var v Verse
		if err := verseRows.Scan(&v.ID, &v.SongID, &v.Type, &v.VerseOrder,
			&v.Content, &v.Chords); err != nil {
			return nil, err
		}
		// The same song can appear in several items, and each needs its own
		// copy of the verses.
		for _, s := range songIndex[v.SongID] {
			s.Verses = append(s.Verses, v)
		}
	}
	return out, verseRows.Err()
}

type CollectionInput struct {
	Name        string  `json:"name"`
	ServiceDate *string `json:"service_date"`
	Notes       *string `json:"notes"`
	TemplateID  *string `json:"template_id"`
	BgAudioPath *string `json:"bg_audio_path"`
}

func (r *Repo) Create(ctx context.Context, orgID, userID uuid.UUID, in CollectionInput) (Collection, error) {
	var c Collection
	var serviceDate *time.Time
	err := r.pool.QueryRow(ctx, `
		insert into collections (org_id, created_by, name, service_date, notes)
		values ($1, $2, $3, $4, $5)
		returning id, name, service_date, notes, template_id, bg_audio_path, created_at`,
		orgID, userID, in.Name, parseDate(in.ServiceDate), in.Notes,
	).Scan(&c.ID, &c.Name, &serviceDate, &c.Notes, &c.TemplateID, &c.BgAudioPath, &c.CreatedAt)
	if err != nil {
		return Collection{}, err
	}
	c.Items = []Item{}
	if serviceDate != nil {
		f := serviceDate.Format("2006-01-02")
		c.ServiceDate = &f
	}
	return c, nil
}

// Update applies only the fields present in the request.
//
// Every column uses coalesce against a "clear" flag, so the client can
// distinguish "leave this alone" from "set this to null" — the difference
// between not touching the background audio and removing it.
func (r *Repo) Update(ctx context.Context, orgID, id uuid.UUID, in map[string]any) error {
	sets := []string{}
	args := []any{id, orgID}

	add := func(column string, value any) {
		args = append(args, value)
		sets = append(sets, column+" = $"+strconv.Itoa(len(args)))
	}

	if v, ok := in["name"]; ok {
		add("name", v)
	}
	if v, ok := in["service_date"]; ok {
		add("service_date", parseDateAny(v))
	}
	if v, ok := in["notes"]; ok {
		add("notes", v)
	}
	if v, ok := in["template_id"]; ok {
		add("template_id", v)
	}
	if v, ok := in["bg_audio_path"]; ok {
		add("bg_audio_path", v)
	}
	if len(sets) == 0 {
		return nil
	}

	tag, err := r.pool.Exec(ctx,
		`update collections set `+strings.Join(sets, ", ")+` where id = $1 and org_id = $2`, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return httpx.ErrNotFound
	}
	return nil
}

func (r *Repo) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `delete from collections where id = $1 and org_id = $2`, id, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return httpx.ErrNotFound
	}
	return nil
}

type ItemInput struct {
	ItemType        string          `json:"item_type"`
	SongID          *uuid.UUID      `json:"song_id"`
	TemplateID      *string         `json:"template_id"`
	ContentJSON     json.RawMessage `json:"content_json"`
	Notes           *string         `json:"notes"`
	AutoAdvanceSecs *int            `json:"auto_advance_secs"`
}

// AddItems appends items to a collection, continuing the existing order.
func (r *Repo) AddItems(ctx context.Context, orgID, collectionID uuid.UUID, inputs []ItemInput) error {
	if err := r.assertOwned(ctx, orgID, collectionID); err != nil {
		return err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var next int
	if err := tx.QueryRow(ctx,
		`select coalesce(max(item_order) + 1, 0) from collection_items where collection_id = $1`,
		collectionID).Scan(&next); err != nil {
		return err
	}

	for _, in := range inputs {
		itemType := in.ItemType
		if itemType == "" {
			itemType = "song"
		}
		var content any
		if len(in.ContentJSON) > 0 {
			content = []byte(in.ContentJSON)
		}
		if _, err := tx.Exec(ctx, `
			insert into collection_items
			    (collection_id, song_id, template_id, item_order, item_type,
			     content_json, notes, auto_advance_secs)
			values ($1, $2, $3, $4, $5, $6, $7, $8)`,
			collectionID, in.SongID, in.TemplateID, next, itemType,
			content, in.Notes, in.AutoAdvanceSecs,
		); err != nil {
			return err
		}
		next++
	}

	return tx.Commit(ctx)
}

// UpdateItem changes the per-item settings: design, note, auto-advance.
func (r *Repo) UpdateItem(ctx context.Context, orgID, itemID uuid.UUID, in map[string]any) error {
	sets := []string{}
	args := []any{itemID, orgID}

	add := func(column string, value any) {
		args = append(args, value)
		sets = append(sets, column+" = $"+strconv.Itoa(len(args)))
	}

	if v, ok := in["template_id"]; ok {
		add("template_id", v)
	}
	if v, ok := in["notes"]; ok {
		add("notes", v)
	}
	if v, ok := in["auto_advance_secs"]; ok {
		add("auto_advance_secs", toIntPtr(v))
	}
	if len(sets) == 0 {
		return nil
	}

	tag, err := r.pool.Exec(ctx, `
		update collection_items set `+strings.Join(sets, ", ")+`
		where id = $1
		  and collection_id in (select id from collections where org_id = $2)`, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return httpx.ErrNotFound
	}
	return nil
}

func (r *Repo) DeleteItem(ctx context.Context, orgID, itemID uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `
		delete from collection_items
		where id = $1 and collection_id in (select id from collections where org_id = $2)`,
		itemID, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return httpx.ErrNotFound
	}
	return nil
}

// Reorder writes a whole new running order in one transaction, so the set list
// is never briefly left with two items claiming the same position.
func (r *Repo) Reorder(ctx context.Context, orgID, collectionID uuid.UUID, ids []uuid.UUID) error {
	if err := r.assertOwned(ctx, orgID, collectionID); err != nil {
		return err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for i, id := range ids {
		if _, err := tx.Exec(ctx, `
			update collection_items set item_order = $3
			where id = $1 and collection_id = $2`, id, collectionID, i,
		); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *Repo) assertOwned(ctx context.Context, orgID, collectionID uuid.UUID) error {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`select exists (select 1 from collections where id = $1 and org_id = $2)`,
		collectionID, orgID).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return httpx.ErrNotFound
	}
	return nil
}

// ── helpers ─────────────────────────────────────────────────────────────────

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func parseDate(s *string) *time.Time {
	if s == nil || *s == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02", (*s)[:min(10, len(*s))])
	if err != nil {
		return nil
	}
	return &t
}

func parseDateAny(v any) any {
	s, ok := v.(string)
	if !ok {
		return nil
	}
	return parseDate(&s)
}

func toIntPtr(v any) any {
	switch n := v.(type) {
	case float64:
		i := int(n)
		return &i
	case int:
		return &n
	default:
		return nil
	}
}

// ── HTTP ────────────────────────────────────────────────────────────────────

type Handler struct{ repo *Repo }

func NewHandler(repo *Repo) *Handler { return &Handler{repo: repo} }

func (h *Handler) Router() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Patch("/{id}", h.update)
	r.Delete("/{id}", h.delete)
	r.Post("/{id}/items", h.addItems)
	r.Put("/{id}/order", h.reorder)
	r.Patch("/items/{itemID}", h.updateItem)
	r.Delete("/items/{itemID}", h.deleteItem)
	return r
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	out, err := h.repo.List(r.Context(), auth.MustOrgID(r.Context()))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var in CollectionInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		httpx.WriteError(w, httpx.Fail(http.StatusBadRequest, "invalid_name", "el nombre es obligatorio"))
		return
	}
	userID, _ := auth.UserID(r.Context())
	out, err := h.repo.Create(r.Context(), auth.MustOrgID(r.Context()), userID, in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	patch, err := decodePatch(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.repo.Update(r.Context(), auth.MustOrgID(r.Context()), id, patch); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.NoContent(w)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.repo.Delete(r.Context(), auth.MustOrgID(r.Context()), id); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.NoContent(w)
}

// addItems takes either one item or a batch, so importing a slide deck is one
// request instead of one per slide.
func (h *Handler) addItems(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}

	var body struct {
		Items []ItemInput `json:"items"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if len(body.Items) == 0 {
		httpx.WriteError(w, httpx.Fail(http.StatusBadRequest, "no_items", "no hay elementos que agregar"))
		return
	}
	if err := h.repo.AddItems(r.Context(), auth.MustOrgID(r.Context()), id, body.Items); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.NoContent(w)
}

func (h *Handler) updateItem(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "itemID")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	patch, err := decodePatch(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.repo.UpdateItem(r.Context(), auth.MustOrgID(r.Context()), id, patch); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.NoContent(w)
}

func (h *Handler) deleteItem(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "itemID")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.repo.DeleteItem(r.Context(), auth.MustOrgID(r.Context()), id); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.NoContent(w)
}

func (h *Handler) reorder(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	var body struct {
		ItemIDs []uuid.UUID `json:"item_ids"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.repo.Reorder(r.Context(), auth.MustOrgID(r.Context()), id, body.ItemIDs); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.NoContent(w)
}

func pathID(r *http.Request, param string) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, param))
	if err != nil {
		return uuid.Nil, httpx.Fail(http.StatusBadRequest, "bad_id", "id inválido")
	}
	return id, nil
}

// decodePatch keeps the raw map so a handler can tell an absent field from one
// explicitly set to null.
func decodePatch(r *http.Request) (map[string]any, error) {
	var patch map[string]any
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(&patch); err != nil {
		return nil, httpx.Fail(http.StatusBadRequest, "bad_request", "cuerpo inválido")
	}
	return patch, nil
}
