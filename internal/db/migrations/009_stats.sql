-- How the app is spreading, counted without knowing who anyone is.
--
-- Two questions had no answer: how many people download Introduce and from
-- which countries, and how many copies are actually opened. GitHub counts
-- downloads per file and nothing else, and nothing counted copies at all.
--
-- A row holds a country, never the address it came from: the IP is looked up
-- and thrown away before the insert. install_id is a random id the app makes
-- for itself on each computer, so copies can be counted without a name or an
-- email attached.
create table if not exists app_events (
    id          bigserial   primary key,
    -- 'download' (the website's download button) or 'app_open'.
    name        text        not null,
    platform    text        not null default '',
    app_version text        not null default '',
    -- ISO 3166 alpha-2, or '' when the address could not be placed.
    country     text        not null default '',
    install_id  uuid,
    org_id      uuid        references organizations (id) on delete set null,
    created_at  timestamptz not null default now()
);

-- Every read is "this kind of event, this stretch of time".
create index if not exists app_events_name_time_idx
    on app_events (name, created_at desc);

-- Where a church is, taken from the address its account was created from.
-- Only the country, for the same reason as above.
alter table organizations
    add column if not exists country text not null default '';
