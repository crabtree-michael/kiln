-- The steward module's one table: a record that a Working ticket's stalled
-- agent was poked, and when. Adapter-layer state, not board state (03 I8) — it
-- only remembers which stalls have been poked so the mechanical sweep can tell a
-- second stall from a first.
--
-- One row per ticket while it is Working (ticket_id is the key): a ticket is
-- poked at most once per Working episode, and the row is deleted when the ticket
-- leaves Working or is escalated to Blocked. worker_id/ticket_id are carried by
-- value (the board owns those tables); there are no FKs across the module edge.

CREATE TABLE steward_pokes (
  ticket_id text PRIMARY KEY,
  worker_id text NOT NULL,
  poked_at  timestamptz NOT NULL
);
