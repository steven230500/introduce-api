package org

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/steven230500/introduce-api/internal/auth"
	"github.com/steven230500/introduce-api/internal/httpx"
)

// A church always keeps someone who can approve people and hand out password
// codes. Without one, nobody new could ever join.
var errLastAdmin = httpx.Fail(http.StatusConflict, "last_admin",
	"la iglesia necesita al menos un administrador")

// target is a member an administrator is acting on, with its church's other
// administrators counted inside the same transaction.
type target struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	OrgID       uuid.UUID
	Role        string
	OtherAdmins int
}

// lockTarget checks that actorID is an active administrator of memberID's
// church and that memberID is an active member of it, and locks that church's
// administrator rows until the transaction ends. The lock is what stops two
// administrators removing each other at the same moment and leaving none.
func lockTarget(ctx context.Context, tx pgx.Tx, actorID, memberID uuid.UUID) (target, error) {
	var t target
	err := tx.QueryRow(ctx, `
		select target.id, target.user_id, target.org_id, target.role
		from organization_members target
		join organization_members actor
		  on actor.org_id = target.org_id
		 and actor.user_id = $1
		 and actor.role = 'admin'
		 and actor.status = 'active'
		where target.id = $2 and target.status = 'active'`, actorID, memberID,
	).Scan(&t.ID, &t.UserID, &t.OrgID, &t.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, httpx.ErrForbidden
	}
	if err != nil {
		return t, err
	}

	rows, err := tx.Query(ctx, `
		select id from organization_members
		where org_id = $1 and role = 'admin' and status = 'active'
		for update`, t.OrgID)
	if err != nil {
		return t, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return t, err
		}
		if id != t.ID {
			t.OtherAdmins++
		}
	}
	return t, rows.Err()
}

// SetRole makes a member an administrator or takes it away.
func (r *Repo) SetRole(ctx context.Context, actorID, memberID uuid.UUID, role string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	t, err := lockTarget(ctx, tx, actorID, memberID)
	if err != nil {
		return err
	}
	if role == "member" && t.Role == "admin" && t.OtherAdmins == 0 {
		return errLastAdmin
	}
	if _, err := tx.Exec(ctx,
		`update organization_members set role = $2 where id = $1`, t.ID, role); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RemoveMember takes someone out of the church. Their sessions end too: an
// access token still names the church for a few minutes, and a refresh must
// not hand them a new one.
func (r *Repo) RemoveMember(ctx context.Context, actorID, memberID uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	t, err := lockTarget(ctx, tx, actorID, memberID)
	if err != nil {
		return err
	}
	if t.Role == "admin" && t.OtherAdmins == 0 {
		return errLastAdmin
	}
	if _, err := tx.Exec(ctx, `delete from organization_members where id = $1`, t.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		update refresh_tokens set revoked_at = now()
		where user_id = $1 and revoked_at is null`, t.UserID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// MemberUser is the account behind a member an administrator may act on.
func (r *Repo) MemberUser(ctx context.Context, actorID, memberID uuid.UUID) (uuid.UUID, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	t, err := lockTarget(ctx, tx, actorID, memberID)
	if err != nil {
		return uuid.Nil, err
	}
	return t.UserID, tx.Commit(ctx)
}

func memberParam(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return uuid.Nil, httpx.Fail(http.StatusBadRequest, "bad_id", "id inválido")
	}
	return id, nil
}

func (h *Handler) setRole(w http.ResponseWriter, r *http.Request) {
	memberID, err := memberParam(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	var body struct {
		Role string `json:"role"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if body.Role != "admin" && body.Role != "member" {
		httpx.WriteError(w, httpx.Fail(http.StatusBadRequest, "invalid_role", "rol inválido"))
		return
	}
	actorID, _ := auth.UserID(r.Context())
	if err := h.repo.SetRole(r.Context(), actorID, memberID, body.Role); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.NoContent(w)
}

func (h *Handler) removeMember(w http.ResponseWriter, r *http.Request) {
	memberID, err := memberParam(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	actorID, _ := auth.UserID(r.Context())
	if err := h.repo.RemoveMember(r.Context(), actorID, memberID); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.NoContent(w)
}

// resetCode makes a one-time code the member uses, with their email, to set a
// new password. The administrator reads it out or sends it; nothing is mailed.
func (h *Handler) resetCode(w http.ResponseWriter, r *http.Request) {
	memberID, err := memberParam(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	actorID, _ := auth.UserID(r.Context())
	userID, err := h.repo.MemberUser(r.Context(), actorID, memberID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}

	code, hash, err := auth.NewResetCode()
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	expiresAt := time.Now().Add(auth.ResetCodeTTL)
	if err := h.auth.StoreResetCode(r.Context(), userID, actorID, hash, expiresAt); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusCreated, map[string]any{"code": code, "expires_at": expiresAt})
}
