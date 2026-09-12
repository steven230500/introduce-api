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
// The bytes live on this server's disk; this row is the index the app browses.
type Item struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	URL         string    `json:"url"`
	StoragePath string    `json:"storage_path"`
	MediaType   string    `json:"media_type"`
	SizeBytes   *int64    `json:"size_bytes"`
	CreatedAt   time.Time `json:"created_at"`
}

type Repo struct{ pool *pgxpool.Pool }

func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

func (r *Repo) List(ctx context.Context, orgID uuid.UUID) ([]Item, error) {
	rows, err := r.pool.Query(ctx, `
		select id, name, url, storage_path, media_type, size_bytes, created_at
		from media_items where org_id = $1 order by created_at desc`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Item{}
	for rows.Next() {
		var m Item
		if err := rows.Scan(&m.ID, &m.Name, &m.URL, &m.StoragePath,
			&m.MediaType, &m.SizeBytes, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *Repo) Create(ctx context.Context, orgID, userID uuid.UUID, m Item) (Item, error) {
	err := r.pool.QueryRow(ctx, `
		insert into media_items (org_id, created_by, name, url, storage_path, media_type, size_bytes)
		values ($1, $2, $3, $4, $5, $6, $7)
		returning id, name, url, storage_path, media_type, size_bytes, created_at`,
		orgID, userID, m.Name, m.URL, m.StoragePath, m.MediaType, m.SizeBytes,
	).Scan(&m.ID, &m.Name, &m.URL, &m.StoragePath, &m.MediaType, &m.SizeBytes, &m.CreatedAt)
	return m, err
}

// Take removes the row and returns it, so the caller knows which file on disk
// to delete. Row first, then bytes: a row pointing at a missing file shows as a
// broken thumbnail, while a file with no row is invisible and never freed.
func (r *Repo) Take(ctx context.Context, orgID, id uuid.UUID) (Item, error) {
	var m Item
	err := r.pool.QueryRow(ctx, `
		delete from media_items where id = $1 and org_id = $2
		returning id, name, url, storage_path, media_type, size_bytes, created_at`,
		id, orgID,
	).Scan(&m.ID, &m.Name, &m.URL, &m.StoragePath, &m.MediaType, &m.SizeBytes, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Item{}, httpx.ErrNotFound
	}
	return m, err
}
