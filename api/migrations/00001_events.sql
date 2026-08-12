-- +goose Up
-- +goose StatementBegin

-- gen_random_uuid() is core since Postgres 13, so no pgcrypto extension needed.
create table events (
    id                 uuid primary key default gen_random_uuid(),
    slug               text        not null unique,
    title              text        not null,

    -- IANA zone name. The window below is expressed in *this* zone's local
    -- dates and times; absolute slot instants are derived from the pair.
    organizer_tz       text        not null,

    window_start       date        not null,
    window_end         date        not null,
    day_start          time        not null default '09:00',
    day_end            time        not null default '17:00',
    slot_minutes       int         not null default 30,

    status             text        not null default 'open',
    decided_slot_start timestamptz,

    created_at         timestamptz not null default now(),
    expires_at         timestamptz not null default now() + interval '60 days',

    constraint events_status_valid   check (status in ('open', 'decided', 'expired')),
    constraint events_window_ordered check (window_end >= window_start),
    constraint events_day_ordered    check (day_end > day_start),
    constraint events_slot_minutes   check (slot_minutes between 5 and 480),
    constraint events_title_present  check (length(btrim(title)) > 0),

    -- A decided event must name the slot it landed on, and an open one must
    -- not. Enforced here because the alternative is trusting every write path.
    constraint events_decided_has_slot check (
        (status = 'decided') = (decided_slot_start is not null)
    )
);

-- Expiry sweeps scan by date; the partial index keeps that cheap once the
-- table is mostly dead events.
create index events_expires_at_idx on events (expires_at) where status <> 'expired';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop table events;
-- +goose StatementEnd
