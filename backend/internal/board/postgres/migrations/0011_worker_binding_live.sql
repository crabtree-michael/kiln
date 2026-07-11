-- I3 (the worker-binding invariant) now binds only LIVE rows.
-- (docs/superpowers/specs/2026-07-11-delete-blocked-ticket-design.md)
--
-- Deleting a blocked ticket archives it AND releases the worker it holds, so the
-- archived row is blocked with worker_id NULL — which the original board-wide I3,
-- (state IN ('working','blocked')) = (worker_id IS NOT NULL), forbids. Archive is
-- orthogonal to State (it never rewrites it, only stamps archived_at), so the row
-- keeps state='blocked' as historical truth; scoping I3 to archived_at IS NULL
-- lets the off-board row hold no worker while the live-board invariant — every
-- ticket the user and the pull actually see — is unchanged. I4 (blocked_reason)
-- is untouched: the archived row keeps its reason, so it still satisfies I4.
--
-- The original I3 is table-level CHECK #1 in 0001_board.sql, so Postgres named it
-- `tickets_check` (I4 is `tickets_check1`). Drop it and re-add the scoped form in
-- one statement so the invariant is never absent mid-migration. Migrations apply
-- in filename order (02 §14).

ALTER TABLE tickets
  DROP CONSTRAINT tickets_check,
  ADD CONSTRAINT tickets_worker_binding_live
    CHECK (archived_at IS NOT NULL
           OR (state IN ('working','blocked')) = (worker_id IS NOT NULL));
