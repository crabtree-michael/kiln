-- Close the events-queue duplicate-delivery gap (architecture audit 3.1). An
-- agent turn's completion emit (INSERT agent.turn_completed) and its phase->done
-- write live in two separate statements, so a crash between them leaves the turn
-- non-terminal; the poller re-runs stepCheckTurn, the provider turn is still
-- terminal, and the completion event is emitted a SECOND time. The events table
-- had only a bigserial PK -- no dedup key -- so the duplicate was accepted and
-- the brain ran a second pass on the same completion.
--
-- Stamp each mechanically-emitted event with its source turn's outbox id and
-- insert ON CONFLICT DO NOTHING, so a replayed emit collapses onto the first and
-- the completion becomes exactly-once. Nullable + partial unique index -> the
-- runtime's own human.message rows (which carry no key) are untouched; only
-- agent.turn_completed is deduped. Mirrors the notifications idempotency
-- precedent (0005_notifications_idempotency.sql).

ALTER TABLE events ADD COLUMN idempotency_key bigint NULL;

CREATE UNIQUE INDEX events_idempotency_key
  ON events (idempotency_key)
  WHERE idempotency_key IS NOT NULL;
