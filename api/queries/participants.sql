-- name: CreateParticipant :one
insert into participants (
    event_id, token_hash, display_name, tz, role, is_organizer
) values (
    $1, $2, $3, $4, $5, $6
)
returning *;

-- name: GetParticipantByToken :one
select * from participants where event_id = $1 and token_hash = $2;

-- name: ListParticipants :many
select * from participants where event_id = $1 order by created_at, id;

-- name: MarkParticipantResponded :exec
update participants set responded_at = now() where id = $1;
