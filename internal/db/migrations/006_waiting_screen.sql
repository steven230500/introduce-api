-- What the screen shows while nothing is on it.
--
-- Before a service, between the worship set and the sermon, while the offering
-- is taken: the room looks at the projector and until now it saw black. A
-- waiting screen is an animated scene with the church's name and perhaps a
-- clock, and every window has to agree on whether it is up and which one.
--
-- jsonb rather than columns because a scene carries its own settings - a
-- title, a line under it, whether to show the time - and the set of scenes
-- will keep growing. A new setting should not need a migration.
alter table presentation_state
    add column if not exists waiting jsonb;
