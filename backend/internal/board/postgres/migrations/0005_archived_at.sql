-- archived_at: a soft-deleted ticket, set by the brain's delete_ticket
-- (06 §4 amended / docs/superpowers/specs/2026-07-04-brain-crud-tool-consolidation-design.md).
-- An archived ticket is retained for history but is invisible to every read
-- (Snapshot, GetTicket) and to the pull (NextReadyTicket), and every targeted
-- operation treats it as not-found. Nullable; null means live.
-- Migrations apply in filename order (02 §14).

ALTER TABLE tickets
  ADD COLUMN archived_at timestamptz;

-- The pull's partial index and the render reads all filter archived_at IS NULL;
-- a partial index over live rows keeps those scans cheap as archived rows grow.
CREATE INDEX tickets_live ON tickets (state) WHERE archived_at IS NULL;
