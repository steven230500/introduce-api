// Package org owns organizations and who belongs to them.
package org

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/steven230500/introduce-api/internal/auth"
	"github.com/steven230500/introduce-api/internal/httpx"
)

type Organization struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type Member struct {
	ID          uuid.UUID `json:"id"`
	OrgID       uuid.UUID `json:"org_id"`
	UserID      uuid.UUID `json:"user_id"`
	Role        string    `json:"role"`
	Status      string    `json:"status"`
	Email       *string   `json:"email"`
	DisplayName *string   `json:"display_name"`
	JoinedAt    time.Time `json:"joined_at"`
}

type Repo struct{ pool *pgxpool.Pool }

func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

// Membership returns the caller's organization and their standing in it.
func (r *Repo) Membership(ctx context.Context, userID uuid.UUID) (Organization, Member, error) {
	var o Organization
	var m Member
	err := r.pool.QueryRow(ctx, `
		select o.id, o.name, o.created_at,
		       m.id, m.org_id, m.user_id, m.role, m.status, m.email, u.display_name, m.joined_at
		from organization_members m
		join organizations o on o.id = m.org_id
		join users u on u.id = m.user_id
		where m.user_id = $1 and m.status <> 'rejected'
		order by m.joined_at desc
		limit 1`, userID,
	).Scan(&o.ID, &o.Name, &o.CreatedAt,
		&m.ID, &m.OrgID, &m.UserID, &m.Role, &m.Status, &m.Email, &m.DisplayName, &m.JoinedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, Member{}, httpx.ErrNotFound
	}
	return o, m, err
}

// Create makes an organization and enrolls the caller as its active admin.
//
// Both statements run in one transaction: an organization with no admin would
// be unreachable and could never approve anyone.
func (r *Repo) Create(ctx context.Context, userID uuid.UUID, name, email string) (Organization, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Organization{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var o Organization
	if err := tx.QueryRow(ctx,
		`insert into organizations (name) values ($1) returning id, name, created_at`, name,
	).Scan(&o.ID, &o.Name, &o.CreatedAt); err != nil {
		return Organization{}, err
	}

	if _, err := tx.Exec(ctx, `
		insert into organization_members (org_id, user_id, role, status, email)
		values ($1, $2, 'admin', 'active', $3)`, o.ID, userID, email,
	); err != nil {
		return Organization{}, err
	}

	return o, tx.Commit(ctx)
}

func (r *Repo) Search(ctx context.Context, query string) ([]Organization, error) {
	rows, err := r.pool.Query(ctx, `
		select id, name, created_at from organizations
		where name ilike '%' || $1 || '%'
		order by name
		limit 10`, strings.TrimSpace(query))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Organization{}
	for rows.Next() {
		var o Organization
		if err := rows.Scan(&o.ID, &o.Name, &o.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// RequestJoin records a pending membership.
func (r *Repo) RequestJoin(ctx context.Context, orgID, userID uuid.UUID, email string) error {
	_, err := r.pool.Exec(ctx, `
		insert into organization_members (org_id, user_id, role, status, email)
		values ($1, $2, 'member', 'pending', $3)
		on conflict (org_id, user_id) do update set status = 'pending'`,
		orgID, userID, email)
	return err
}

func (r *Repo) Members(ctx context.Context, orgID uuid.UUID, status string) ([]Member, error) {
	rows, err := r.pool.Query(ctx, `
		select m.id, m.org_id, m.user_id, m.role, m.status, m.email, u.display_name, m.joined_at
		from organization_members m
		join users u on u.id = m.user_id
		where m.org_id = $1 and m.status = $2
		order by m.joined_at`, orgID, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.ID, &m.OrgID, &m.UserID, &m.Role, &m.Status,
			&m.Email, &m.DisplayName, &m.JoinedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SetStatus approves or rejects a pending member, but only for an admin of the
// same organization. The org_id and role checks are in the statement itself, so
// there is no window between checking and writing.
func (r *Repo) SetStatus(ctx context.Context, adminID, memberID uuid.UUID, status string) error {
	tag, err := r.pool.Exec(ctx, `
		update organization_members target
		set status = $3
		where target.id = $2
		  and exists (
		      select 1 from organization_members admin
		      where admin.user_id = $1
		        and admin.org_id = target.org_id
		        and admin.role = 'admin'
		        and admin.status = 'active'
		  )`, adminID, memberID, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return httpx.ErrForbidden
	}
	return nil
}

// IsAdmin reports whether the user administers the organization.
func (r *Repo) IsAdmin(ctx context.Context, userID, orgID uuid.UUID) (bool, error) {
	var ok bool
	err := r.pool.QueryRow(ctx, `
		select exists (
			select 1 from organization_members
			where user_id = $1 and org_id = $2 and role = 'admin' and status = 'active'
		)`, userID, orgID).Scan(&ok)
	return ok, err
}

// ── HTTP ────────────────────────────────────────────────────────────────────

type Handler struct {
	repo *Repo
	auth *auth.Repo
}

// Palette returns the colours this church has saved.
func (r *Repo) Palette(ctx context.Context, orgID uuid.UUID) ([]string, error) {
	var raw []byte
	err := r.pool.QueryRow(ctx,
		`select coalesce(palette, '[]'::jsonb) from organizations where id = $1`, orgID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, httpx.ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	colors := []string{}
	if err := json.Unmarshal(raw, &colors); err != nil {
		// A row we cannot read is not worth failing a service over; the church
		// simply sees the built-in swatches.
		return []string{}, nil
	}
	return colors, nil
}

// SetPalette replaces the saved colours.
func (r *Repo) SetPalette(ctx context.Context, orgID uuid.UUID, colors []string) error {
	encoded, err := json.Marshal(colors)
	if err != nil {
		return err
	}
	tag, err := r.pool.Exec(ctx,
		`update organizations set palette = $2 where id = $1`, orgID, encoded)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return httpx.ErrNotFound
	}
	return nil
}

func NewHandler(repo *Repo, authRepo *auth.Repo) *Handler {
	return &Handler{repo: repo, auth: authRepo}
}

func (h *Handler) Router() chi.Router {
	r := chi.NewRouter()
	r.Get("/me", h.membership)
	r.Post("/", h.create)
	r.Get("/search", h.search)
	r.Post("/join", h.join)
	r.Get("/members", h.members)
	r.Get("/pending", h.pending)
	r.Post("/members/{id}/approve", h.approve)
	r.Post("/members/{id}/reject", h.reject)
	r.Get("/palette", h.palette)
	r.Put("/palette", h.setPalette)
	return r
}

// hexColor is the only shape a stored colour may take.
//
// The designs read these straight into a renderer, so anything else would
// surface as a blank slide during a service rather than as an error here.
var hexColor = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// maxPaletteColors keeps one church from turning the picker into a wall.
const maxPaletteColors = 24

func (h *Handler) palette(w http.ResponseWriter, r *http.Request) {
	orgID, ok := auth.OrgID(r.Context())
	if !ok {
		httpx.WriteError(w, httpx.ErrForbidden)
		return
	}
	colors, err := h.repo.Palette(r.Context(), orgID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"colors": colors})
}

func (h *Handler) setPalette(w http.ResponseWriter, r *http.Request) {
	orgID, ok := auth.OrgID(r.Context())
	if !ok {
		httpx.WriteError(w, httpx.ErrForbidden)
		return
	}

	var body struct {
		Colors []string `json:"colors"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if len(body.Colors) > maxPaletteColors {
		httpx.WriteError(w, httpx.Fail(http.StatusBadRequest, "too_many_colors",
			"demasiados colores guardados"))
		return
	}

	cleaned := make([]string, 0, len(body.Colors))
	seen := map[string]bool{}
	for _, c := range body.Colors {
		c = strings.ToUpper(strings.TrimSpace(c))
		if !hexColor.MatchString(c) {
			httpx.WriteError(w, httpx.Fail(http.StatusBadRequest, "bad_color",
				"color inválido: "+c))
			return
		}
		if seen[c] {
			continue
		}
		seen[c] = true
		cleaned = append(cleaned, c)
	}

	if err := h.repo.SetPalette(r.Context(), orgID, cleaned); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"colors": cleaned})
}

func (h *Handler) membership(w http.ResponseWriter, r *http.Request) {
	userID, _ := auth.UserID(r.Context())
	o, m, err := h.repo.Membership(r.Context(), userID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"organization": o, "member": m})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		httpx.WriteError(w, httpx.Fail(http.StatusBadRequest, "invalid_name", "el nombre no puede estar vacío"))
		return
	}

	userID, _ := auth.UserID(r.Context())
	user, err := h.auth.UserByID(r.Context(), userID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}

	o, err := h.repo.Create(r.Context(), userID, strings.TrimSpace(body.Name), user.Email)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, o)
}

func (h *Handler) search(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if strings.TrimSpace(query) == "" {
		httpx.JSON(w, http.StatusOK, []Organization{})
		return
	}
	out, err := h.repo.Search(r.Context(), query)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (h *Handler) join(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OrgID uuid.UUID `json:"org_id"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	userID, _ := auth.UserID(r.Context())
	user, err := h.auth.UserByID(r.Context(), userID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.repo.RequestJoin(r.Context(), body.OrgID, userID, user.Email); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.NoContent(w)
}

func (h *Handler) members(w http.ResponseWriter, r *http.Request) { h.listByStatus(w, r, "active") }
func (h *Handler) pending(w http.ResponseWriter, r *http.Request) { h.listByStatus(w, r, "pending") }

func (h *Handler) listByStatus(w http.ResponseWriter, r *http.Request, status string) {
	orgID, ok := auth.OrgID(r.Context())
	if !ok {
		httpx.JSON(w, http.StatusOK, []Member{})
		return
	}
	out, err := h.repo.Members(r.Context(), orgID, status)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (h *Handler) approve(w http.ResponseWriter, r *http.Request) { h.setStatus(w, r, "active") }
func (h *Handler) reject(w http.ResponseWriter, r *http.Request)  { h.setStatus(w, r, "rejected") }

func (h *Handler) setStatus(w http.ResponseWriter, r *http.Request, status string) {
	memberID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, httpx.Fail(http.StatusBadRequest, "bad_id", "id inválido"))
		return
	}
	adminID, _ := auth.UserID(r.Context())
	if err := h.repo.SetStatus(r.Context(), adminID, memberID, status); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.NoContent(w)
}
