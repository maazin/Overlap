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
set status = 'decided', decided_slot_start = $2
where id = $1
returning *;

-- name: ReopenEvent :one
update events
set status = 'open', decided_slot_start = null
where id = $1
returning *;
