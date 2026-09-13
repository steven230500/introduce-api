-- The colours a church uses, kept once for the whole organisation.
--
-- Designs used to pick from twenty values written into the app, so a church
-- with its own colours had no way to use them and every design guessed at the
-- same blue separately.
alter table organizations
    add column if not exists palette jsonb not null default '[]'::jsonb;
