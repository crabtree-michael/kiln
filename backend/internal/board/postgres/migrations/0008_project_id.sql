-- project_id: tenant column (11 §3). Nullable until boot-time adoption
-- backfills every row and flips it NOT NULL (cmd/kiln bootstrap, a later
-- task). Plain uuid carried by value — no FK across module migration sets
-- (03 I8).
-- Migrations apply in filename order (02 §14).

ALTER TABLE tickets ADD COLUMN project_id uuid;
ALTER TABLE workers ADD COLUMN project_id uuid;
ALTER TABLE outbox  ADD COLUMN project_id uuid;

-- Snapshot/render reads and the pull scope by project once adopted; a
-- partial index over live rows keeps those scans cheap as archived rows grow.
CREATE INDEX tickets_project_live ON tickets (project_id) WHERE archived_at IS NULL;

-- Replaces tickets_ready_pull_order (0001_board.sql) with a project-scoped
-- version so the pull can select the next ready ticket per project.
DROP INDEX tickets_ready_pull_order;
CREATE INDEX tickets_ready_pull_order
    ON tickets (project_id, priority DESC, ready_at ASC, id ASC)
    WHERE state = 'ready';

CREATE INDEX workers_by_project ON workers (project_id);
CREATE INDEX outbox_due_project ON outbox (project_id, id) WHERE status = 'pending';
