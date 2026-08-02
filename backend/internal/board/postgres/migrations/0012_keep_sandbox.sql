-- keep_sandbox: the per-ticket sandbox option, set by the user from the ticket
-- detail sheet (POST /api/tickets/{id}/sandbox).
--
-- A ticket leaving Developing normally emits agent.release, which destroys and
-- recreates the slot's worker so the next conversation starts from a fresh
-- workspace (05 §4) — the sandbox, and everything uncommitted in it, is gone.
-- With this set the board skips that emission, so the sandbox survives and an
-- agent can keep working in the same workspace across turns.
--
-- NOT NULL DEFAULT false: recycling stays the default, so every existing row and
-- every ticket the brain creates behaves exactly as before.
-- Migrations apply in filename order (02 §14).

ALTER TABLE tickets ADD COLUMN keep_sandbox boolean NOT NULL DEFAULT false;
