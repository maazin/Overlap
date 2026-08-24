-- name: CreateGroup :one
insert into groups (slug, name, created_from)
values ($1, $2, $3)
returning *;

-- name: GetGroupBySlug :one
select * from groups where slug = $1;

-- name: GetGroupByID :one
select * from groups where id = $1;

-- name: CreateGroupMember :one
insert into group_members (group_id, token_hash, display_name, tz, default_role)
values ($1, $2, $3, $4, $5)
returning *;

-- name: ClaimGroupMember :one
update group_members set token_hash = $2
where id = $1
returning *;

-- name: GetGroupMemberByToken :one
select * from group_members where group_id = $1 and token_hash = $2;

-- name: GetGroupMemberByID :one
select * from group_members where id = $1 and group_id = $2;

-- name: ListGroupMembers :many
select * from group_members where group_id = $1 order by joined_at, id;

-- name: SetGroupMemberCalendar :exec
update group_members
set calendar_source = $2, calendar_url = $3
where id = $1;

-- name: DeleteGroupMemberBusyBlocks :exec
delete from group_member_busy_blocks where group_member_id = $1;

-- name: InsertGroupMemberBusyBlocks :exec
insert into group_member_busy_blocks (group_member_id, start_ts, end_ts, source)
select $1, unnest($2::timestamptz[]), unnest($3::timestamptz[]), $4;

-- name: ListGroupMemberBusyBlocks :many
select * from group_member_busy_blocks where group_member_id = $1 order by start_ts;

-- name: ListGroupEvents :many
select * from events where group_id = $1 order by created_at desc;

-- name: ListGroupDecisions :many
-- Past decisions the group actually settled on, oldest information excluded by
-- nothing here -- decay is applied in Go, where the half-life constant lives
-- next to the rest of the scoring model rather than duplicated in SQL.
select decided_slot_start, decided_at
from events
where group_id = $1 and status = 'decided' and decided_slot_start is not null and decided_at is not null
order by decided_at desc;

-- name: GetParticipantByGroupMember :one
select * from participants where event_id = $1 and group_member_id = $2;

-- name: CreateParticipantFromGroupMember :one
insert into participants (
    event_id, token_hash, display_name, tz, role, group_member_id
) values (
    $1, $2, $3, $4, $5, $6
)
returning *;

-- name: RotateParticipantToken :one
update participants set token_hash = $2
where id = $1
returning *;

-- name: GetGroupMemberByIDUnscoped :one
-- Used only where the caller has already established which group this id
-- belongs to through some other verified path (a checked token, an id read
-- back from a row already scoped to the right group). ClaimEventSeat is the
-- one caller: by the time it runs, the event's own group_id has already
-- picked out which group this member id must belong to.
select * from group_members where id = $1;
