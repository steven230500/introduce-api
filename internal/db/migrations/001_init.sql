-- Schema for Introduce, owned end to end by this service.
--
-- Everything a church works on belongs to an organization. Rows carry org_id
-- for scoping and created_by for attribution; authorization lives in Go
-- middleware rather than in row-level policies, so that one place decides who
-- may touch what.

create extension if not exists pgcrypto;
create extension if not exists pg_trgm;
create extension if not exists citext;

-- ── Identity ────────────────────────────────────────────────────────────────

create table users (
    id            uuid primary key default gen_random_uuid(),
    email         citext      not null unique,
    password_hash text        not null,
    display_name  text,
    created_at    timestamptz not null default now(),
    updated_at    timestamptz not null default now()
);

-- Refresh tokens are stored hashed, never in the clear, so a database leak
-- cannot be replayed as a login. One row per issued token, rotated on use.
create table refresh_tokens (
    id         uuid primary key default gen_random_uuid(),
    user_id    uuid        not null references users (id) on delete cascade,
    token_hash bytea       not null unique,
    expires_at timestamptz not null,
    revoked_at timestamptz,
    created_at timestamptz not null default now()
);

create index refresh_tokens_user_idx on refresh_tokens (user_id);
create index refresh_tokens_expiry_idx on refresh_tokens (expires_at);

-- ── Organizations ───────────────────────────────────────────────────────────

create table organizations (
    id         uuid primary key default gen_random_uuid(),
    name       text        not null,
    created_at timestamptz not null default now()
);

create table organization_members (
    id        uuid primary key default gen_random_uuid(),
    org_id    uuid        not null references organizations (id) on delete cascade,
    user_id   uuid        not null references users (id) on delete cascade,
    role      text        not null default 'member' check (role in ('admin', 'member')),
    status    text        not null default 'pending' check (status in ('pending', 'active', 'rejected')),
    email     text,
    joined_at timestamptz not null default now(),
    unique (org_id, user_id)
);

create index organization_members_user_idx on organization_members (user_id);

-- ── Songs ───────────────────────────────────────────────────────────────────

create table songs (
    id          uuid primary key default gen_random_uuid(),
    org_id      uuid        not null references organizations (id) on delete cascade,
    created_by  uuid references users (id) on delete set null,
    title       text        not null,
    author      text,
    copyright   text,
    ccli_number text,
    language    text        not null default 'es',
    tags        text[]      not null default '{}',
    created_at  timestamptz not null default now(),
    updated_at  timestamptz not null default now()
);

create index songs_org_idx on songs (org_id);
create index songs_title_trgm_idx on songs using gin (title gin_trgm_ops);

create table verses (
    id          uuid primary key default gen_random_uuid(),
    song_id     uuid    not null references songs (id) on delete cascade,
    type        text    not null check (
        type in ('verse', 'chorus', 'bridge', 'pre-chorus', 'tag', 'intro', 'outro')
    ),
    verse_order integer not null,
    content     text    not null,
    chords      text
);

create index verses_song_idx on verses (song_id, verse_order);

-- ── Slide designs ───────────────────────────────────────────────────────────

create table templates (
    id         uuid primary key default gen_random_uuid(),
    org_id     uuid        not null references organizations (id) on delete cascade,
    created_by uuid references users (id) on delete set null,
    name       text        not null,
    config     jsonb       not null default '{}'::jsonb,
    created_at timestamptz not null default now()
);

create index templates_org_idx on templates (org_id);

-- ── Service plans ───────────────────────────────────────────────────────────

create table collections (
    id            uuid primary key default gen_random_uuid(),
    org_id        uuid        not null references organizations (id) on delete cascade,
    created_by    uuid references users (id) on delete set null,
    name          text        not null,
    service_date  date,
    notes         text,
    -- Text, not a foreign key: it also holds preset ids such as preset_light,
    -- which are defined in the client and have no row here.
    template_id   text,
    bg_audio_path text,
    created_at    timestamptz not null default now()
);

create index collections_org_idx on collections (org_id, service_date desc);

create table collection_items (
    id                uuid primary key default gen_random_uuid(),
    collection_id     uuid    not null references collections (id) on delete cascade,
    song_id           uuid references songs (id) on delete cascade,
    template_id       text,
    item_order        integer not null,
    item_type         text    not null default 'song' check (
        item_type in (
            'song', 'bible_verse', 'sermon', 'free_slide',
            'image_slide', 'video_slide', 'announcement'
        )
    ),
    content_json      jsonb,
    notes             text,
    auto_advance_secs integer
);

create index collection_items_collection_idx on collection_items (collection_id, item_order);

-- ── Live state ──────────────────────────────────────────────────────────────

-- One row per operator. The projector and stage windows follow it over the
-- websocket hub; the row is what a window reads when it first connects.
create table presentation_state (
    user_id             uuid primary key references users (id) on delete cascade,
    collection_id       uuid references collections (id) on delete set null,
    current_item_index  integer     not null default 0,
    current_slide_index integer     not null default 0,
    is_live             boolean     not null default false,
    blank_screen        boolean     not null default false,
    countdown_active    boolean     not null default false,
    countdown_end       timestamptz,
    overlay_visible     boolean     not null default false,
    overlay_text        text,
    updated_at          timestamptz not null default now()
);

-- ── Media ───────────────────────────────────────────────────────────────────

create table media_items (
    id           uuid primary key default gen_random_uuid(),
    org_id       uuid        not null references organizations (id) on delete cascade,
    created_by   uuid references users (id) on delete set null,
    name         text        not null,
    url          text        not null,
    storage_path text        not null,
    media_type   text        not null check (media_type in ('image', 'video')),
    size_bytes   bigint,
    created_at   timestamptz not null default now()
);

create index media_items_org_idx on media_items (org_id, created_at desc);

-- ── Timestamps ──────────────────────────────────────────────────────────────

create or replace function touch_updated_at() returns trigger as $$
begin
    new.updated_at = now();
    return new;
end;
$$ language plpgsql;

create trigger users_touch before update on users
    for each row execute function touch_updated_at();

create trigger songs_touch before update on songs
    for each row execute function touch_updated_at();

create trigger presentation_state_touch before update on presentation_state
    for each row execute function touch_updated_at();
