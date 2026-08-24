-- +goose Up
-- +goose StatementBegin

-- Foreign keys Postgres has to check in reverse on delete, with nothing to
-- check them against.
--
-- A referencing column with no index makes every delete of the referenced row
-- a sequential scan of the referencing table. Neither of these fires on a page
-- load, so they are not a latency problem today; they are the kind that only
-- shows up once the tables are large and something starts expiring old events
-- in bulk, at which point the cause is a long way from the symptom.

-- events.expires_at sweeps and any manual event deletion currently seq-scan
-- groups looking for rows that graduated from the deleted event.
create index groups_created_from_idx on groups (created_from)
    where created_from is not null;

-- participants_event_group_member_idx exists but leads with event_id, so
-- Postgres cannot use it to answer "which participants reference this group
-- member" when a member is removed.
create index participants_group_member_idx on participants (group_member_id)
    where group_member_id is not null;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop index participants_group_member_idx;
drop index groups_created_from_idx;
-- +goose StatementEnd
