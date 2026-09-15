package org

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/steven230500/introduce-api/internal/db"
	"github.com/steven230500/introduce-api/internal/httpx"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return pool
}

type church struct {
	orgID uuid.UUID
	// user id -> member id
	members map[uuid.UUID]uuid.UUID
}

func newUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`insert into users (email, password_hash) values ($1, 'x') returning id`,
		uuid.NewString()+"@test.invalid").Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// newChurch makes a church whose first user is its administrator, plus the
// given number of active members.
func newChurch(t *testing.T, pool *pgxpool.Pool, repo *Repo, members int) (church, uuid.UUID, []uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	admin := newUser(t, pool)
	o, err := repo.Create(ctx, admin, "Iglesia "+uuid.NewString(), "a@test.invalid", "CO")
	if err != nil {
		t.Fatal(err)
	}
	c := church{orgID: o.ID, members: map[uuid.UUID]uuid.UUID{}}
	var ids []uuid.UUID
	for i := 0; i < members; i++ {
		u := newUser(t, pool)
		if _, err := pool.Exec(ctx, `
			insert into organization_members (org_id, user_id, role, status, email)
			values ($1, $2, 'member', 'active', 'm@test.invalid')`, o.ID, u); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, u)
	}
	rows, err := pool.Query(ctx, `select user_id, id from organization_members where org_id = $1`, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var u, m uuid.UUID
		if err := rows.Scan(&u, &m); err != nil {
			t.Fatal(err)
		}
		c.members[u] = m
	}
	return c, admin, ids
}

func apiCode(err error) string {
	var apiErr *httpx.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}
	return ""
}

func TestAnAdministratorCanMakeAnotherOne(t *testing.T) {
	pool := testPool(t)
	repo := NewRepo(pool)
	ctx := context.Background()
	c, admin, members := newChurch(t, pool, repo, 1)

	if err := repo.SetRole(ctx, admin, c.members[members[0]], "admin"); err != nil {
		t.Fatal(err)
	}
	ok, err := repo.IsAdmin(ctx, members[0], c.orgID)
	if err != nil || !ok {
		t.Fatalf("not an admin after being made one: %v %v", ok, err)
	}
	// With two, the first can step down.
	if err := repo.SetRole(ctx, members[0], c.members[admin], "member"); err != nil {
		t.Fatalf("stepping down with another admin: %v", err)
	}
}

func TestTheLastAdministratorCannotBeRemovedOrDemoted(t *testing.T) {
	pool := testPool(t)
	repo := NewRepo(pool)
	ctx := context.Background()
	c, admin, _ := newChurch(t, pool, repo, 1)

	if err := repo.SetRole(ctx, admin, c.members[admin], "member"); apiCode(err) != "last_admin" {
		t.Fatalf("demoting the last admin: %v", err)
	}
	if err := repo.RemoveMember(ctx, admin, c.members[admin]); apiCode(err) != "last_admin" {
		t.Fatalf("removing the last admin: %v", err)
	}
}

func TestAMemberCannotManageTheChurch(t *testing.T) {
	pool := testPool(t)
	repo := NewRepo(pool)
	ctx := context.Background()
	c, admin, members := newChurch(t, pool, repo, 2)

	if err := repo.SetRole(ctx, members[0], c.members[members[0]], "admin"); !errors.Is(err, httpx.ErrForbidden) {
		t.Fatalf("a member made themselves admin: %v", err)
	}
	if err := repo.RemoveMember(ctx, members[0], c.members[admin]); !errors.Is(err, httpx.ErrForbidden) {
		t.Fatalf("a member removed the admin: %v", err)
	}
	if _, err := repo.MemberUser(ctx, members[0], c.members[members[1]]); !errors.Is(err, httpx.ErrForbidden) {
		t.Fatalf("a member could make a password code: %v", err)
	}
}

func TestAnAdministratorOfAnotherChurchCannotTouchThisOne(t *testing.T) {
	pool := testPool(t)
	repo := NewRepo(pool)
	ctx := context.Background()
	c, _, members := newChurch(t, pool, repo, 1)
	_, otherAdmin, _ := newChurch(t, pool, repo, 0)

	if err := repo.RemoveMember(ctx, otherAdmin, c.members[members[0]]); !errors.Is(err, httpx.ErrForbidden) {
		t.Fatalf("cross-church removal: %v", err)
	}
	if _, err := repo.MemberUser(ctx, otherAdmin, c.members[members[0]]); !errors.Is(err, httpx.ErrForbidden) {
		t.Fatalf("cross-church password code: %v", err)
	}
}

func TestARemovedMemberLosesTheChurchAndTheirSessions(t *testing.T) {
	pool := testPool(t)
	repo := NewRepo(pool)
	ctx := context.Background()
	c, admin, members := newChurch(t, pool, repo, 1)
	if _, err := pool.Exec(ctx, `
		insert into refresh_tokens (user_id, token_hash, expires_at)
		values ($1, $2, now() + interval '1 day')`, members[0], []byte(uuid.NewString())); err != nil {
		t.Fatal(err)
	}

	if err := repo.RemoveMember(ctx, admin, c.members[members[0]]); err != nil {
		t.Fatal(err)
	}

	var memberships, liveTokens int
	_ = pool.QueryRow(ctx, `select count(*) from organization_members where user_id = $1`, members[0]).Scan(&memberships)
	_ = pool.QueryRow(ctx, `select count(*) from refresh_tokens where user_id = $1 and revoked_at is null`, members[0]).Scan(&liveTokens)
	if memberships != 0 || liveTokens != 0 {
		t.Fatalf("memberships %d, live tokens %d", memberships, liveTokens)
	}
}

func TestRejectOnlyAnswersARequestAndCannotRemoveSomeoneInside(t *testing.T) {
	pool := testPool(t)
	repo := NewRepo(pool)
	ctx := context.Background()
	c, admin, _ := newChurch(t, pool, repo, 0)

	// Rejecting the admin's own active membership used to leave the church
	// with nobody able to approve anyone.
	if err := repo.SetStatus(ctx, admin, c.members[admin], "rejected"); !errors.Is(err, httpx.ErrForbidden) {
		t.Fatalf("reject on an active admin: %v", err)
	}
	ok, _ := repo.IsAdmin(ctx, admin, c.orgID)
	if !ok {
		t.Fatal("the admin was rejected out of the church")
	}
}
