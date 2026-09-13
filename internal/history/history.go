// Package history keeps the record of what a church put on its screen.
//
// The app decides what counts as "on the screen" and when a stretch of it
// ends; this package only stores those stretches and hands them back by date.
// Keeping the judgement on the client is deliberate: the client is the one that
// knows whether the output was live or blanked, and it is the one that has to
// keep recording when the network is gone.
package history

import (
	"context"
	"fmt"
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

// Event is one stretch of time one item was on air.
type Event struct {
	ID             uuid.UUID  `json:"id"`
	CollectionID   *uuid.UUID `json:"collection_id"`
	CollectionName string     `json:"collection_name"`
	ItemType       string     `json:"item_type"`
	Title          string     `json:"title"`
	SongID         *uuid.UUID `json:"song_id"`
	SongAuthor     *string    `json:"song_author"`
	SongCopyright  *string    `json:"song_copyright"`
	CCLINumber     *string    `json:"ccli_number"`
	StartedAt      time.Time  `json:"started_at"`
	EndedAt        time.Time  `json:"ended_at"`
}

// A batch this size covers a long service recorded entirely offline. Anything
// larger is not a service, and the client is expected to split it.
const maxBatch = 500

// The longest range one request may read. A year of Sundays is a few hundred
// rows; an unbounded range is an easy way to ask the database for everything.
const maxRange = 366 * 24 * time.Hour

var knownTypes = map[string]bool{
	"song": true, "bible_verse": true, "sermon": true, "free_slide": true,
	"image_slide": true, "video_slide": true, "announcement": true,
}

// Validate says what is wrong with an event, in the terms of the event.
func (e Event) Validate() error {
	switch {
	case e.ID == uuid.Nil:
		return fmt.Errorf("an event has no id")
	case !knownTypes[e.ItemType]:
		return fmt.Errorf("unknown item type %q", e.ItemType)
	case e.StartedAt.IsZero() || e.EndedAt.IsZero():
		return fmt.Errorf("event %s has no start or end", e.ID)
	case e.EndedAt.Before(e.StartedAt):
		return fmt.Errorf("event %s ends before it starts", e.ID)
	}
	return nil
}

type Repo struct{ pool *pgxpool.Pool }

func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

// Record stores events, ignoring any it already has.
//
// Idempotent on the client's id, because the client resends whatever it is
// not sure arrived. A service recorded offline and sent twice must count once.
func (r *Repo) Record(ctx context.Context, orgID, userID uuid.UUID, events []Event) (int, error) {
	if len(events) == 0 {
		return 0, nil
	}
	batch := &pgx.Batch{}
	for _, e := range events {
		batch.Queue(`
			insert into projection_log (
				id, org_id, recorded_by, collection_id, collection_name, item_type, title,
				song_id, song_author, song_copyright, ccli_number, started_at, ended_at
			) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
			on conflict (id) do nothing`,
			e.ID, orgID, userID, e.CollectionID, e.CollectionName, e.ItemType, e.Title,
			e.SongID, e.SongAuthor, e.SongCopyright, e.CCLINumber, e.StartedAt, e.EndedAt,
		)
	}
	results := r.pool.SendBatch(ctx, batch)
	defer results.Close()

	stored := 0
	for range events {
		tag, err := results.Exec()
		if err != nil {
			return stored, err
		}
		stored += int(tag.RowsAffected())
	}
	return stored, nil
}

// Between returns a church's events in a range, oldest first, so a service
// reads top to bottom in the order it happened.
func (r *Repo) Between(ctx context.Context, orgID uuid.UUID, from, to time.Time) ([]Event, error) {
	rows, err := r.pool.Query(ctx, `
		select id, collection_id, collection_name, item_type, title,
		       song_id, song_author, song_copyright, ccli_number, started_at, ended_at
		from projection_log
		where org_id = $1 and started_at >= $2 and started_at < $3
		order by started_at asc`, orgID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Event{}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.CollectionID, &e.CollectionName, &e.ItemType, &e.Title,
			&e.SongID, &e.SongAuthor, &e.SongCopyright, &e.CCLINumber, &e.StartedAt, &e.EndedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

type Handler struct{ repo *Repo }

func NewHandler(repo *Repo) *Handler { return &Handler{repo: repo} }

func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.list)
	r.Post("/", h.record)
	return r
}

type recordRequest struct {
	Events []Event `json:"events"`
}

type recordResponse struct {
	Received int `json:"received"`
	Stored   int `json:"stored"`
}

func (h *Handler) record(w http.ResponseWriter, r *http.Request) {
	var body recordRequest
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, httpx.Fail(http.StatusBadRequest, "bad_body", "cuerpo inválido"))
		return
	}
	if len(body.Events) > maxBatch {
		httpx.WriteError(w, httpx.Fail(http.StatusRequestEntityTooLarge, "batch_too_large",
			fmt.Sprintf("se pueden enviar hasta %d eventos por vez", maxBatch)))
		return
	}

	// One bad event refuses the whole batch rather than storing the rest. The
	// client resends a batch it could not deliver, and a batch that half
	// succeeds is one it cannot reason about.
	var problems []string
	for _, e := range body.Events {
		if err := e.Validate(); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		httpx.WriteError(w, httpx.Fail(http.StatusBadRequest, "invalid_events", strings.Join(problems, "; ")))
		return
	}

	userID, _ := auth.UserID(r.Context())
	stored, err := h.repo.Record(r.Context(), auth.MustOrgID(r.Context()), userID, body.Events)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, recordResponse{Received: len(body.Events), Stored: stored})
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	from, to, err := parseRange(r.URL.Query().Get("from"), r.URL.Query().Get("to"), time.Now())
	if err != nil {
		httpx.WriteError(w, httpx.Fail(http.StatusBadRequest, "bad_range", err.Error()))
		return
	}
	events, err := h.repo.Between(r.Context(), auth.MustOrgID(r.Context()), from, to)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, events)
}

// parseRange reads the dates a report asks for.
//
// Missing ends default to the last ninety days, which is the window a
// quarterly licence report needs and a reasonable "recently" for the history.
func parseRange(fromRaw, toRaw string, now time.Time) (time.Time, time.Time, error) {
	to := now
	if toRaw != "" {
		parsed, err := time.Parse(time.RFC3339, toRaw)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("to no es una fecha")
		}
		to = parsed
	}
	from := to.Add(-90 * 24 * time.Hour)
	if fromRaw != "" {
		parsed, err := time.Parse(time.RFC3339, fromRaw)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("from no es una fecha")
		}
		from = parsed
	}
	if !from.Before(to) {
		return time.Time{}, time.Time{}, fmt.Errorf("from tiene que ser antes que to")
	}
	if to.Sub(from) > maxRange {
		return time.Time{}, time.Time{}, fmt.Errorf("el rango no puede pasar de un año")
	}
	return from, to, nil
}
