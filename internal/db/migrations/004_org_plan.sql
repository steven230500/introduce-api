-- Which plan a church is on.
--
-- Storage is the only thing here that costs money per church rather than per
-- deploy: one Sunday's countdown video is larger than everything else an
-- organization will ever write to this database. Every church starts free and
-- the column is raised by hand for now; there is no billing yet, and pretending
-- otherwise in the schema would be inventing a shape before the payments exist.
alter table organizations
    add column if not exists plan text not null default 'free';

-- Uploads are counted per organization on every upload, so the sum has to be
-- cheap. Without this it is a sequential scan of every file every church owns.
create index if not exists media_items_org_size_idx
    on media_items (org_id) include (size_bytes);
