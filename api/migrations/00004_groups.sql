-- +goose Up
-- +goose StatementBegin

create table groups (
    id           uuid        primary key default gen_random_uuid(),
    slug         text        unique not null,
    name         text        not null,
    -- The event this group graduated from, kept only for provenance. Set null
    -- rather than cascading a delete: an event expiring should never take a
    -- persistent group down with it.
    created_from uuid        references events(id) on delete set null,
    created_at   timestamptz not null default now(),

    constraint groups_name_present check (length(btrim(name)) > 0)
);

create table group_members (
    id              uuid        primary key default gen_random_uuid(),
    group_id        uuid        not null references groups(id) on delete cascade,

    -- Nullable, unlike participants.token_hash. Graduation can name a member
    -- before anyone has opened the group link on that person's device, and the
    -- row has to exist to be listed and claimed. A member with no token is
    -- unclaimed, exactly the state "picked from the list, not yet claimed"
    -- needs to be representable in.
    token_hash      bytea,

    display_name    text        not null,
    tz              text        not null,
    default_role    text        not null default 'optional',

    -- Free/busy only, same invariant as participants: never event details.
    calendar_source text        not null default 'none',
    calendar_url    text,

    joined_at       timestamptz not null default now(),

    constraint group_members_role_valid     check (default_role in ('required', 'optional')),
    constraint group_members_calendar_valid check (calendar_source in ('none', 'ics')),
    constraint group_members_name_present   check (length(btrim(display_name)) > 0)
);

-- Partial: only claimed members need a unique token per group. Two unclaimed
-- "Sam" rows can coexist for a moment during graduation without tripping a
-- constraint meant to stop token collisions.
create unique index group_members_group_token_idx
    on group_members (group_id, token_hash) where token_hash is not null;
create index group_members_group_idx on group_members (group_id);

create table group_member_busy_blocks (
    id              uuid        primary key default gen_random_uuid(),
    group_member_id uuid        not null references group_members(id) on delete cascade,
    start_ts        timestamptz not null,
    end_ts          timestamptz not null,
    source          text        not null,
    fetched_at      timestamptz not null default now(),

    constraint gmbb_source_valid check (source in ('ics')),
    constraint gmbb_ordered      check (end_ts > start_ts)
);
create index gmbb_member_idx on group_member_busy_blocks (group_member_id, start_ts);

-- An event belongs to a group, or is a bare one-off link. Set null on delete:
-- the invariant in section 4 of the PRD is that the one-off flow always works
-- standalone, so a group's removal must never be able to orphan-delete an
-- event that already happened.
alter table events add column group_id uuid references groups(id) on delete set null;
create index events_group_idx on events (group_id) where group_id is not null;

-- Records when a decision was made, not just what it was, because the
-- 30-day history half-life needs an age to decay from. decided_slot_start
-- alone says what was picked, not when.
alter table events add column decided_at timestamptz;

-- Links an event participant back to the group member who owns the seat. Only
-- meaningful for events created from a group. The partial unique index is what
-- makes claiming idempotent: a second claim finds the existing row instead of
-- creating a duplicate seat for the same person.
alter table participants add column group_member_id uuid
    references group_members(id) on delete set null;
create unique index participants_event_group_member_idx
    on participants (event_id, group_member_id) where group_member_id is not null;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
alter table participants drop column group_member_id;
alter table events drop column decided_at;
alter table events drop column group_id;
drop table group_member_busy_blocks;
drop table group_members;
drop table groups;
-- +goose StatementEnd
