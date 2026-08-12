-- +goose Up
-- +goose StatementBegin

create table participants (
    id              uuid        primary key default gen_random_uuid(),
    event_id        uuid        not null references events(id) on delete cascade,

    -- SHA-256 of the opaque token the client keeps in localStorage. The token
    -- is a bearer credential: anything holding it can rewrite that person's
    -- availability, and an organizer token will later be able to lock a time.
    -- Tokens are 256 bits of CSPRNG output, so a plain digest is the right
    -- construction here -- a password KDF defends against guessing, and there
    -- is nothing to guess.
    token_hash      bytea       not null,

    display_name    text        not null,
    tz              text        not null,
    role            text        not null default 'optional',
    email           text,
    calendar_source text        not null default 'none',
    is_organizer    boolean     not null default false,

    -- Non-null once this person has submitted. This is what separates "said no
    -- to everything" from "has not answered", a distinction the solver and the
    -- dominance analysis both depend on.
    responded_at    timestamptz,
    created_at      timestamptz not null default now(),

    constraint participants_role_valid     check (role in ('required', 'optional')),
    constraint participants_calendar_valid check (calendar_source in ('none', 'google', 'ics')),
    constraint participants_name_present   check (length(btrim(display_name)) > 0)
);

create unique index participants_event_token_idx on participants (event_id, token_hash);
create index participants_event_idx on participants (event_id);

create table responses (
    participant_id uuid        not null references participants(id) on delete cascade,
    slot_start     timestamptz not null,

    -- 3 preferred, 2 ok, 1 if_needed, 0 no. Deliberately the same ordering as
    -- solver.Tier so the two cannot drift apart.
    tier           smallint    not null,

    -- Where the tier came from. A tier inferred from a calendar must stay
    -- distinguishable from one a person actually stated, and must remain
    -- overridable; treating inferred data as stated data is the fastest way to
    -- lose trust.
    source         text        not null default 'manual',

    primary key (participant_id, slot_start),

    constraint responses_tier_valid   check (tier between 0 and 3),
    constraint responses_source_valid check (source in ('manual', 'calendar', 'coarse'))
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop table responses;
drop table participants;
-- +goose StatementEnd
