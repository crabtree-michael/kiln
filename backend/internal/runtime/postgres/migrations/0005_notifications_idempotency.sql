-- Deterministic completion card (08 §7): AcceptToDone emits a feed.completion
-- outbox row and the runtime turns it into a persistent "done" notification, so
-- completions post to the feed regardless of whether the agent remembered to.
-- Outbox delivery is at-least-once, so the completion handler must dedupe: it
-- stamps the outbox entry id as idempotency_key and inserts ON CONFLICT DO
-- NOTHING. Nullable + partial unique index -> brain/poke rows (which never carry
-- a key) are untouched; only mechanically-posted completion cards are deduped.

ALTER TABLE notifications ADD COLUMN idempotency_key bigint NULL;

CREATE UNIQUE INDEX notifications_idempotency_key
  ON notifications (idempotency_key)
  WHERE idempotency_key IS NOT NULL;
