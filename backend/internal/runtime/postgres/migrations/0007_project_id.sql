-- project_id: tenant column (11 §3). Nullable until boot-time adoption
-- backfills every row and flips it NOT NULL (cmd/kiln bootstrap, a later
-- task). Plain uuid carried by value — no FK across module migration sets
-- (03 I8).
-- Migrations apply in filename order (02 §14).

ALTER TABLE events        ADD COLUMN project_id uuid;
ALTER TABLE messages      ADD COLUMN project_id uuid;
ALTER TABLE notifications ADD COLUMN project_id uuid;

CREATE INDEX events_due_project ON events (project_id, id) WHERE status = 'pending';
CREATE INDEX messages_recent_project ON messages (project_id, id DESC);
CREATE INDEX notifications_recent_project ON notifications (project_id, id DESC);
