// Package stats counts how Introduce spreads: downloads from the website,
// copies of the app being opened, and the churches using it. Everything it
// stores is anonymous except what a church already gave when it signed up.
package stats

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Event is one row of app_events.
type Event struct {
	Name       string
	Platform   string
	AppVersion string
	Country    string
	InstallID  *uuid.UUID
	OrgID      *uuid.UUID
}

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

func (r *Repo) Record(ctx context.Context, e Event) error {
	_, err := r.pool.Exec(ctx, `
		insert into app_events (name, platform, app_version, country, install_id, org_id)
		values ($1, $2, $3, $4, $5, $6)`,
		e.Name, e.Platform, e.AppVersion, e.Country, e.InstallID, e.OrgID)
	return err
}

// Windows is a count over the last week, the last month and all time.
type Windows struct {
	Week  int
	Month int
	Total int
}

// Row is one line of a breakdown: a platform, a country or a version.
type Row struct {
	Label string
	Windows
}

// Church is one organization as the owner of the service sees it.
type Church struct {
	Name           string
	Country        string
	CreatedAt      time.Time
	Plan           string
	Members        int
	Pending        int
	Admins         string
	LastProjection *time.Time
	DaysProjected  int
	LastOpened     *time.Time
}

// Summary is everything the dashboard shows.
type Summary struct {
	Downloads             Windows
	DownloadsByPlatform   []Row
	DownloadsByCountry    []Row
	Copies                Windows
	CopiesByPlatform      []Row
	CopiesByCountry       []Row
	CopiesByVersion       []Row
	Churches              []Church
	Accounts              int
	AccountsWithoutChurch int
}

// Summarize reads the dashboard in one go. Copies are distinct install ids,
// so a computer that opens the app ten times a day is still one copy.
func (r *Repo) Summarize(ctx context.Context) (Summary, error) {
	var s Summary
	var err error

	if err = r.pool.QueryRow(ctx, `
		select count(*) filter (where created_at > now() - interval '7 days'),
		       count(*) filter (where created_at > now() - interval '30 days'),
		       count(*)
		from app_events where name = 'download'`,
	).Scan(&s.Downloads.Week, &s.Downloads.Month, &s.Downloads.Total); err != nil {
		return s, err
	}
	if s.DownloadsByPlatform, err = r.rows(ctx, downloadsBy("platform")); err != nil {
		return s, err
	}
	if s.DownloadsByCountry, err = r.rows(ctx, downloadsBy("country")); err != nil {
		return s, err
	}

	if err = r.pool.QueryRow(ctx, `
		select count(distinct install_id) filter (where created_at > now() - interval '7 days'),
		       count(distinct install_id) filter (where created_at > now() - interval '30 days'),
		       count(distinct install_id)
		from app_events where name = 'app_open'`,
	).Scan(&s.Copies.Week, &s.Copies.Month, &s.Copies.Total); err != nil {
		return s, err
	}
	if s.CopiesByPlatform, err = r.rows(ctx, copiesBy("platform")); err != nil {
		return s, err
	}
	if s.CopiesByCountry, err = r.rows(ctx, copiesBy("country")); err != nil {
		return s, err
	}
	if s.CopiesByVersion, err = r.rows(ctx, copiesBy("app_version")); err != nil {
		return s, err
	}

	if s.Churches, err = r.churches(ctx); err != nil {
		return s, err
	}

	err = r.pool.QueryRow(ctx, `
		select count(*),
		       count(*) filter (where not exists (
		           select 1 from organization_members m
		           where m.user_id = u.id and m.status = 'active'))
		from users u`,
	).Scan(&s.Accounts, &s.AccountsWithoutChurch)
	return s, err
}

// The column is one of three fixed names chosen in this file, never input.
func downloadsBy(column string) string {
	return `
		select ` + column + `,
		       count(*) filter (where created_at > now() - interval '7 days'),
		       count(*) filter (where created_at > now() - interval '30 days'),
		       count(*)
		from app_events where name = 'download'
		group by 1 order by 4 desc, 1 limit 40`
}

func copiesBy(column string) string {
	return `
		select ` + column + `,
		       count(distinct install_id) filter (where created_at > now() - interval '7 days'),
		       count(distinct install_id) filter (where created_at > now() - interval '30 days'),
		       count(distinct install_id)
		from app_events where name = 'app_open'
		group by 1 order by 3 desc, 4 desc, 1 limit 40`
}

func (r *Repo) rows(ctx context.Context, query string) ([]Row, error) {
	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Row{}
	for rows.Next() {
		var row Row
		if err := rows.Scan(&row.Label, &row.Week, &row.Month, &row.Total); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// churches lists every organization, newest first. A church created before
// countries were recorded takes the country its copies of the app last opened
// from.
func (r *Repo) churches(ctx context.Context) ([]Church, error) {
	rows, err := r.pool.Query(ctx, `
		select o.name,
		       coalesce(nullif(o.country, ''),
		                (select e.country from app_events e
		                 where e.org_id = o.id and e.country <> ''
		                 order by e.created_at desc limit 1),
		                ''),
		       o.created_at,
		       o.plan,
		       (select count(*) from organization_members m
		        where m.org_id = o.id and m.status = 'active'),
		       (select count(*) from organization_members m
		        where m.org_id = o.id and m.status = 'pending'),
		       coalesce((select string_agg(m.email, ', ' order by m.joined_at)
		                 from organization_members m
		                 where m.org_id = o.id and m.role = 'admin' and m.status = 'active'), ''),
		       (select max(p.ended_at) from projection_log p where p.org_id = o.id),
		       (select count(distinct date_trunc('day', p.started_at))
		        from projection_log p
		        where p.org_id = o.id and p.started_at > now() - interval '30 days'),
		       (select max(e.created_at) from app_events e
		        where e.org_id = o.id and e.name = 'app_open')
		from organizations o
		order by o.created_at desc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Church{}
	for rows.Next() {
		var c Church
		if err := rows.Scan(&c.Name, &c.Country, &c.CreatedAt, &c.Plan, &c.Members, &c.Pending,
			&c.Admins, &c.LastProjection, &c.DaysProjected, &c.LastOpened); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
