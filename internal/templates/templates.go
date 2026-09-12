// Package templates owns slide designs.
//
// A design is a name plus an opaque config blob. The client owns the shape of
// that blob, so the server stores it as jsonb and never interprets it: adding a
// field to a design must not require a deploy here.
package templates

import (
	"context"
	"encoding/json"
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

type Template struct {
	ID        uuid.UUID       `json:"id"`
	Name      string          `json:"name"`
	Config    json.RawMessage `json:"config"`
	CreatedAt time.Time       `json:"created_at"`
}

type Input struct {
	Name   string          `json:"name"`
	Config json.RawMessage `json:"config"`
}

type Repo struct{ pool *pgxpool.Pool }

func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

func (r *Repo) List(ctx context.Context, orgID uuid.UUID) ([]Template, error) {
	rows, err := r.pool.Query(ctx, `
		select id, name, config, created_at from templates
		where org_id = $1 order by created_at`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Template{}
	for rows.Next() {
		var t Template
		if err := rows.Scan(&t.ID, &t.Name, &t.Config, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *Repo) Create(ctx context.Context, orgID, userID uuid.UUID, in Input) (Template, error) {
	var t Template
	err := r.pool.QueryRow(ctx, `
		insert into templates (org_id, created_by, name, config)
		values ($1, $2, $3, $4)
		returning id, name, config, created_at`,
		orgID, userID, in.Name, configOrEmpty(in.Config),
	).Scan(&t.ID, &t.Name, &t.Config, &t.CreatedAt)
	return t, err
}

func (r *Repo) Update(ctx context.Context, orgID, id uuid.UUID, in Input) (Template, error) {
	var t Template
	err := r.pool.QueryRow(ctx, `
		update templates set name = $3, config = $4
		where id = $1 and org_id = $2
		returning id, name, config, created_at`,
		id, orgID, in.Name, configOrEmpty(in.Config),
	).Scan(&t.ID, &t.Name, &t.Config, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Template{}, httpx.ErrNotFound
	}
	return t, err
}

func (r *Repo) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `delete from templates where id = $1 and org_id = $2`, id, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return httpx.ErrNotFound
	}
	return nil
}

func configOrEmpty(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return []byte("{}")
	}
	return raw
}

// ── HTTP ────────────────────────────────────────────────────────────────────

type Handler struct{ repo *Repo }

func NewHandler(repo *Repo) *Handler { return &Handler{repo: repo} }

func (h *Handler) Router() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Put("/{id}", h.update)
	r.Delete("/{id}", h.delete)
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
	in, err := decodeInput(r)
	if err != nil {
		httpx.WriteError(w, err)
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
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, httpx.Fail(http.StatusBadRequest, "bad_id", "id inválido"))
		return
	}
	in, err := decodeInput(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	out, err := h.repo.Update(r.Context(), auth.MustOrgID(r.Context()), id, in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, httpx.Fail(http.StatusBadRequest, "bad_id", "id inválido"))
		return
	}
	if err := h.repo.Delete(r.Context(), auth.MustOrgID(r.Context()), id); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.NoContent(w)
}

func decodeInput(r *http.Request) (Input, error) {
	var in Input
	if err := httpx.Decode(r, &in); err != nil {
		return Input{}, err
	}
	if strings.TrimSpace(in.Name) == "" {
		return Input{}, httpx.Fail(http.StatusBadRequest, "invalid_name", "el nombre es obligatorio")
	}
	return in, nil
}
