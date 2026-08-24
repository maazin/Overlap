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

-- name: SetCalendarSource :exec
update participants
set calendar_source = $2, calendar_url = $3
where id = $1;

-- name: DeleteBusyBlocks :exec
delete from busy_blocks where participant_id = $1;

-- name: InsertBusyBlocks :exec
insert into busy_blocks (participant_id, start_ts, end_ts, source)
select $1, unnest($2::timestamptz[]), unnest($3::timestamptz[]), $4;

-- name: ListBusyBlocks :many
select * from busy_blocks where participant_id = $1 order by start_ts;

-- name: LinkParticipantToGroupMember :one
update participants set group_member_id = $2
where id = $1
returning *;
