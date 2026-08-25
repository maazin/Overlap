-- name: CreateEvent :one
insert into events (
    slug, title, organizer_tz,
    window_start, window_end, day_start, day_end, slot_minutes
) values (
    $1, $2, $3, $4, $5, $6, $7, $8
)
returning *;

-- name: GetEventBySlug :one
select * from events where slug = $1;

-- name: DecideEvent :one
update events
set status = 'decided', decided_slot_start = $2, decided_at = now()
where id = $1
returning *;

-- name: CreateEventInGroup :one
insert into events (
    slug, title, organizer_tz,
    window_start, window_end, day_start, day_end, slot_minutes, group_id
) values (
    $1, $2, $3, $4, $5, $6, $7, $8, $9
)
returning *;

-- name: ReopenEvent :one
update events
set status = 'open', decided_slot_start = null, decided_at = null
where id = $1
returning *;

-- name: LinkEventToGroup :one
update events set group_id = $2
where id = $1
returning *;

-- Deletes rather than tombstones. The privacy note in the README promises the
-- data goes away when the link expires, and a row marked 'expired' still holds
-- every name and every busy interval. Participants, responses and busy blocks
-- follow through `on delete cascade`; groups survive, because both references
-- into events are `on delete set null`.
--
-- The limit bounds one pass so a first sweep over a long-neglected table
-- cannot hold a transaction open across millions of rows. The caller loops
-- until a pass comes back short.
-- name: DeleteExpiredEvents :execrows
delete from events
where id in (
    select id from events
    where expires_at < now()
    order by expires_at
    limit $1
);
