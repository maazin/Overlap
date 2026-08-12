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
