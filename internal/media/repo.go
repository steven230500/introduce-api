package media

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/steven230500/introduce-api/internal/httpx"
)

// Item is one file in an organization's media library.
//
// The bytes live in the configured store; this row is the index the app browses.
type Item struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	URL         string    `json:"url"`
	StoragePath string    `json:"storage_path"`
	MediaType   string    `json:"media_type"`
	SizeBytes   *int64    `json:"size_bytes"`
	CreatedAt   time.Time `json:"created_at"`

	// Role is RoleLibrary for a file put on the screen by itself, and
	// RoleBackground for one that sits behind the text of a design.
	Role string `json:"role"`

	// Width, Height and DurationMs are recorded for backgrounds, which are
	// checked against them. DurationMs is nil for a still.
	Width      *int `json:"width,omitempty"`
	Height     *int `json:"height,omitempty"`
	DurationMs *int `json:"duration_ms,omitempty"`

	// PosterURL is a still frame of a video background. PosterPath is where it
	// is stored, which only the server needs.
	PosterURL  *string `json:"poster_url,omitempty"`
	PosterPath *string `json:"-"`
}

const (
	RoleLibrary    = "library"
	RoleBackground = "background"
)

type Repo struct{ pool *pgxpool.Pool }

func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

const itemColumns = `id, name, url, storage_path, media_type, size_bytes, created_at,
	role, width, height, duration_ms, poster_url, poster_path`

type scanner interface{ Scan(dest ...any) error }

func scanItem(row scanner) (Item, error) {
	var m Item
	err := row.Scan(&m.ID, &m.Name, &m.URL, &m.StoragePath, &m.MediaType, &m.SizeBytes,
		&m.CreatedAt, &m.Role, &m.Width, &m.Height, &m.DurationMs, &m.PosterURL, &m.PosterPath)
	return m, err
}

// List returns the files with one role, newest first.
func (r *Repo) List(ctx context.Context, orgID uuid.UUID, role string) ([]Item, error) {
	rows, err := r.pool.Query(ctx, `
		select `+itemColumns+`
		from media_items where org_id = $1 and role = $2 order by created_at desc`, orgID, role)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Item{}
	for rows.Next() {
		m, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *Repo) Create(ctx context.Context, orgID, userID uuid.UUID, m Item) (Item, error) {
	if m.Role == "" {
		m.Role = RoleLibrary
	}
	return scanItem(r.pool.QueryRow(ctx, `
		insert into media_items (org_id, created_by, name, url, storage_path, media_type, size_bytes,
		                         role, width, height, duration_ms, poster_url, poster_path)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		returning `+itemColumns,
		orgID, userID, m.Name, m.URL, m.StoragePath, m.MediaType, m.SizeBytes,
		m.Role, m.Width, m.Height, m.DurationMs, m.PosterURL, m.PosterPath,
	))
}

// Take removes the row and returns it, so the caller knows which files in the
// store to delete. Row first, then bytes: a row pointing at a missing file
// shows as a broken thumbnail, while a file with no row is invisible and never
// freed.
func (r *Repo) Take(ctx context.Context, orgID, id uuid.UUID) (Item, error) {
	m, err := scanItem(r.pool.QueryRow(ctx, `
		delete from media_items where id = $1 and org_id = $2
		returning `+itemColumns,
		id, orgID,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return Item{}, httpx.ErrNotFound
	}
	return m, err
}

// Usage is how much room an organization has taken and which plan it is on.
//
// One round trip rather than two: this runs on every upload, before the bytes
// are accepted, and an upload is already the slowest thing the API does.
func (r *Repo) Usage(ctx context.Context, orgID uuid.UUID) (used int64, planName string, err error) {
	err = r.pool.QueryRow(ctx, `
		select coalesce(sum(m.size_bytes), 0)::bigint,
		       coalesce(max(o.plan), 'free')
		from organizations o
		left join media_items m on m.org_id = o.id
		where o.id = $1`, orgID).Scan(&used, &planName)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "free", httpx.ErrNotFound
	}
	return used, planName, err
}
