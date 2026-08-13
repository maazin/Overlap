-- +goose Up
-- +goose StatementBegin

create table busy_blocks (
    id             uuid        primary key default gen_random_uuid(),
    participant_id uuid        not null references participants(id) on delete cascade,

    start_ts       timestamptz not null,
    end_ts         timestamptz not null,

    -- Where the block came from. Only ever 'google' or 'ics': these rows record
    -- that someone is committed, never what they are committed to. No title,
    -- no description, no attendees. That is a privacy commitment stated on the
    -- landing page, so there is deliberately nowhere here to put such a value
    -- even if some future code tried to.
    source         text        not null,

    -- When the feed was last read, so a proposal built on stale data can say so
    -- rather than quietly pretending to be current.
    fetched_at     timestamptz not null default now(),

    constraint busy_blocks_source_valid check (source in ('google', 'ics')),
    constraint busy_blocks_ordered      check (end_ts > start_ts)
);

create index busy_blocks_participant_idx on busy_blocks (participant_id, start_ts);

-- The URL of a subscribed feed, so it can be refreshed later without asking
-- again. Kept on the participant rather than in busy_blocks because it belongs
-- to the person, not to any one block.
alter table participants add column calendar_url text;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
alter table participants drop column calendar_url;
drop table busy_blocks;
-- +goose StatementEnd
