-- A note only the platform sees.
--
-- The overlay an operator can already show goes to the projector, which is
-- everyone. There was no way to tell the person preaching that they have five
-- minutes left without telling the congregation too.
alter table presentation_state
    add column if not exists stage_message text;

-- The handful of notices a church shows over and over: the children going out,
-- the offering, where the bathrooms are. They were typed from scratch every
-- time, mid-service, with the room waiting.
alter table organizations
    add column if not exists notices jsonb not null default '[]'::jsonb;
