-- Backgrounds a church adds for its designs.
--
-- A background is a file in the media library with a different job, so it is
-- a row there rather than a table of its own: it counts against the same
-- storage, is deleted the same way, and the plan limits need no second sum.
-- `role` keeps the two lists apart, so a loop meant to sit behind lyrics does
-- not appear among the photos an operator puts on the screen by themselves.
--
-- The dimensions and length are stored because they are what the background
-- standard is checked against, and because the app shows them: "1920 × 1080,
-- 30 s" tells an operator which of two similar loops is the one they want.
--
-- A video background carries a still frame of itself. Every place that shows a
-- design without playing it - thumbnails, the set list, the stage display -
-- shows that frame, rather than opening a video player per thumbnail.
alter table media_items
    add column if not exists role        text not null default 'library',
    add column if not exists width       integer,
    add column if not exists height      integer,
    add column if not exists duration_ms integer,
    add column if not exists poster_url  text,
    add column if not exists poster_path text;

do $$
begin
    if not exists (select 1 from pg_constraint where conname = 'media_items_role_check') then
        alter table media_items
            add constraint media_items_role_check check (role in ('library', 'background'));
    end if;
end $$;

create index if not exists media_items_org_role_idx
    on media_items (org_id, role, created_at desc);
