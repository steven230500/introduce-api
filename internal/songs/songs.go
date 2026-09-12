// Package songs owns the song library and the verses inside each song.
package songs

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/steven230500/introduce-api/internal/auth"
	"github.com/steven230500/introduce-api/internal/httpx"
)

type Verse struct {
	ID         uuid.UUID `json:"id"`
	SongID     uuid.UUID `json:"song_id"`
	Type       string    `json:"type"`
	VerseOrder int       `json:"verse_order"`
	Content    string    `json:"content"`
	Chords     *string   `json:"chords"`
}

type Song struct {
	ID         uuid.UUID `json:"id"`
	Title      string    `json:"title"`
	Author     *string   `json:"author"`
	Copyright  *string   `json:"copyright"`
	CCLINumber *string   `json:"ccli_number"`
	Language   string    `json:"language"`
	Tags       []string  `json:"tags"`
	CreatedAt  time.Time `json:"created_at"`
	Verses     []Verse   `json:"verses"`
}

// Input is the body accepted when creating or replacing a song.
type Input struct {
	Title      string   `json:"title"`
	Author     *string  `json:"author"`
	Copyright  *string  `json:"copyright"`
	CCLINumber *string  `json:"ccli_number"`
	Language   string   `json:"language"`
	Tags       []string `json:"tags"`
	Verses     []struct {
		Type       string  `json:"type"`
		VerseOrder int     `json:"verse_order"`
		Content    string  `json:"content"`
		Chords     *string `json:"chords"`
	} `json:"verses"`
}

type Repo struct{ pool *pgxpool.Pool }

func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

// List returns the organization's songs with their verses.
//
// Verses come back in a second query keyed by song, rather than a join: a join
// would repeat every song column once per verse, and these lists are read on
// every keystroke of the search box.
func (r *Repo) List(ctx context.Context, orgID uuid.UUID, search string) ([]Song, error) {
	search = strings.TrimSpace(search)
	rows, err := r.pool.Query(ctx, `
		select id, title, author, copyright, ccli_number, language, tags, created_at
		from songs
		where org_id = $1
		  and ($2 = '' or title ilike '%' || $2 || '%' or coalesce(author, '') ilike '%' || $2 || '%')
		order by title`, orgID, search)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	songs := []Song{}
	index := map[uuid.UUID]int{}
	for rows.Next() {
		var s Song
		if err := rows.Scan(&s.ID, &s.Title, &s.Author, &s.Copyright,
			&s.CCLINumber, &s.Language, &s.Tags, &s.CreatedAt); err != nil {
			return nil, err
		}
		s.Verses = []Verse{}
		index[s.ID] = len(songs)
		songs = append(songs, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(songs) == 0 {
		return songs, nil
	}

	ids := make([]uuid.UUID, 0, len(songs))
	for id := range index {
		ids = append(ids, id)
	}

	verseRows, err := r.pool.Query(ctx, `
		select id, song_id, type, verse_order, content, chords
		from verses
		where song_id = any($1)
		order by song_id, verse_order`, ids)
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
		if i, ok := index[v.SongID]; ok {
			songs[i].Verses = append(songs[i].Verses, v)
		}
	}
	return songs, verseRows.Err()
}

func (r *Repo) Get(ctx context.Context, orgID, id uuid.UUID) (Song, error) {
	var s Song
	err := r.pool.QueryRow(ctx, `
		select id, title, author, copyright, ccli_number, language, tags, created_at
		from songs where id = $1 and org_id = $2`, id, orgID,
	).Scan(&s.ID, &s.Title, &s.Author, &s.Copyright, &s.CCLINumber,
		&s.Language, &s.Tags, &s.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Song{}, httpx.ErrNotFound
	}
	if err != nil {
		return Song{}, err
	}

	rows, err := r.pool.Query(ctx, `
		select id, song_id, type, verse_order, content, chords
		from verses where song_id = $1 order by verse_order`, id)
	if err != nil {
		return Song{}, err
	}
	defer rows.Close()

	s.Verses = []Verse{}
	for rows.Next() {
		var v Verse
		if err := rows.Scan(&v.ID, &v.SongID, &v.Type, &v.VerseOrder, &v.Content, &v.Chords); err != nil {
			return Song{}, err
		}
		s.Verses = append(s.Verses, v)
	}
	return s, rows.Err()
}

// Save creates a song, or replaces one wholesale when id is non-nil.
//
// Verses are deleted and reinserted rather than diffed, because the editor
// hands back the whole list and reordering makes a diff meaningless.
func (r *Repo) Save(ctx context.Context, orgID, userID uuid.UUID, id *uuid.UUID, in Input) (Song, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Song{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	language := in.Language
	if language == "" {
		language = "es"
	}
	tags := in.Tags
	if tags == nil {
		tags = []string{}
	}

	var songID uuid.UUID
	if id == nil {
		err = tx.QueryRow(ctx, `
			insert into songs (org_id, created_by, title, author, copyright, ccli_number, language, tags)
			values ($1, $2, $3, $4, $5, $6, $7, $8)
			returning id`,
			orgID, userID, in.Title, in.Author, in.Copyright, in.CCLINumber, language, tags,
		).Scan(&songID)
	} else {
		err = tx.QueryRow(ctx, `
			update songs
			set title = $3, author = $4, copyright = $5, ccli_number = $6, language = $7, tags = $8
			where id = $1 and org_id = $2
			returning id`,
			*id, orgID, in.Title, in.Author, in.Copyright, in.CCLINumber, language, tags,
		).Scan(&songID)
		if errors.Is(err, pgx.ErrNoRows) {
			return Song{}, httpx.ErrNotFound
		}
	}
	if err != nil {
		return Song{}, err
	}

	if _, err := tx.Exec(ctx, `delete from verses where song_id = $1`, songID); err != nil {
		return Song{}, err
	}
	for i, v := range in.Verses {
		order := v.VerseOrder
		if order == 0 {
			order = i
		}
		if _, err := tx.Exec(ctx, `
			insert into verses (song_id, type, verse_order, content, chords)
			values ($1, $2, $3, $4, $5)`,
			songID, v.Type, order, v.Content, v.Chords,
		); err != nil {
			return Song{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return Song{}, err
	}
	return r.Get(ctx, orgID, songID)
}

func (r *Repo) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `delete from songs where id = $1 and org_id = $2`, id, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return httpx.ErrNotFound
	}
	return nil
}

// ── HTTP ────────────────────────────────────────────────────────────────────

type Handler struct{ repo *Repo }

func NewHandler(repo *Repo) *Handler { return &Handler{repo: repo} }

func (h *Handler) Router() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Get("/{id}", h.get)
	r.Put("/{id}", h.update)
	r.Delete("/{id}", h.delete)
	return r
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	out, err := h.repo.List(r.Context(), auth.MustOrgID(r.Context()), r.URL.Query().Get("search"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	out, err := h.repo.Get(r.Context(), auth.MustOrgID(r.Context()), id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var in Input
	if err := httpx.Decode(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if strings.TrimSpace(in.Title) == "" {
		httpx.WriteError(w, httpx.Fail(http.StatusBadRequest, "invalid_title", "el título es obligatorio"))
		return
	}
	userID, _ := auth.UserID(r.Context())
	out, err := h.repo.Save(r.Context(), auth.MustOrgID(r.Context()), userID, nil, in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	var in Input
	if err := httpx.Decode(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	userID, _ := auth.UserID(r.Context())
	out, err := h.repo.Save(r.Context(), auth.MustOrgID(r.Context()), userID, &id, in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
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

func parseID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return uuid.Nil, httpx.Fail(http.StatusBadRequest, "bad_id", "id inválido")
	}
	return id, nil
}
