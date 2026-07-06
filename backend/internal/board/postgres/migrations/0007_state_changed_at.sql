-- state_changed_at: when the ticket last entered its current state (03 §2.2).
-- Distinct from updated_at, which bumps on *any* mutation (a Working->Working
-- nudge via SendToAgent included). This stamp advances only when `state` itself
-- changes, so "time in status" reads the real time in the current column and is
-- unaffected by nudges. Set on insert (DEFAULT now()) and in UpdateTicket only
-- when the state transitions.
-- Migrations apply in filename order (02 §14).

ALTER TABLE tickets
  ADD COLUMN state_changed_at timestamptz NOT NULL DEFAULT now();

-- Backfill existing rows from updated_at — the best available approximation of
-- when they entered their current state, so live tickets keep a sensible age
-- across the migration rather than all resetting to migration time.
UPDATE tickets SET state_changed_at = updated_at;
