-- name: DeleteResponsesForParticipant :exec
delete from responses where participant_id = $1;

-- name: InsertResponses :exec
insert into responses (participant_id, slot_start, tier, source)
select
    $1,
    unnest($2::timestamptz[]),
    unnest($3::smallint[]),
    unnest($4::text[]);

-- name: ListResponsesForParticipant :many
select * from responses where participant_id = $1 order by slot_start;

-- name: ListResponsesForEvent :many
select r.*
from responses r
join participants p on p.id = r.participant_id
where p.event_id = $1
order by r.participant_id, r.slot_start;
