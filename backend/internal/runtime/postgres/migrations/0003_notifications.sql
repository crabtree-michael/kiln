-- Brain-authored notifications (08 §3, §7): the "update"/"preview" feed cards.
-- Owned by the runtime module, alongside the events queue (0001_events.sql) and
-- the transcript (0002_messages.sql) — the runtime is the module facing the
-- client and is a second outbox writer (08 §7): posting/retracting/seeing a
-- notification appends a feed.updated outbox row in the same transaction.
-- Append-only in spirit; a row is never deleted, only stamped seen_at (user
-- has caught up) or retracted_at (the brain withdrew it).

CREATE TABLE notifications (
  id          bigserial PRIMARY KEY,
  kind        text NOT NULL CHECK (kind IN ('update','preview')),
  ticket_id   text NULL,
  body        text NOT NULL,
  image_url   text NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  seen_at     timestamptz NULL,
  retracted_at timestamptz NULL
);

-- Feed assembly reads unseen notifications newest-first (08 §3); the id DESC
-- index serves that scan and the MarkSeen high-water lookup at the same index.
CREATE INDEX notifications_recent ON notifications (id DESC);
