-- How long each part of a service is meant to take, and how long it is taking.
--
-- planned_secs is the operator's plan for one item: four minutes for a song,
-- thirty-five for the sermon. It is usually filled in from a rehearsal rather
-- than typed, and it is what the stage display counts the current item
-- against, so the preacher can see they are running long before the worship
-- leader has to wave at them.
--
-- The limits keep a slip of the keyboard - 0, or 40000 seconds - from turning
-- the plan into nonsense for everyone reading it.
alter table collection_items
    add column if not exists planned_secs integer;

do $$
begin
    if not exists (select 1 from pg_constraint where conname = 'collection_items_planned_secs_check') then
        alter table collection_items
            add constraint collection_items_planned_secs_check
            check (planned_secs is null or planned_secs between 1 and 21600);
    end if;
end $$;

-- When the item on the screen started, whether this is a rehearsal, and the
-- plan it is measured against, carried to every window the way the waiting
-- screen is: as the client sent it, since the server has no opinion on timing.
alter table presentation_state
    add column if not exists timing jsonb;
