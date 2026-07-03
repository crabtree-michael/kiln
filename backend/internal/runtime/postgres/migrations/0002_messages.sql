-- The persisted transcript (07 §3): user/kiln chat history. Owned by the
-- runtime module, alongside the events queue (0001_events.sql) — it already
-- owns the client-facing conversation surfaces (04 §7). Append-only in v1;
-- no edits, no deletes. Doubles as the brain's conversation memory
-- (ConversationReader.Recent — 06 §3.2), so nothing here may block reads.

CREATE TABLE messages (
  id         bigserial PRIMARY KEY,
  role       text  NOT NULL CHECK (role IN ('user','kiln')),
  text       text  NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

-- Serves both "most recent n" (GET /api/messages, id DESC then reversed to
-- oldest-first) and "last n for the brain" (Recent) at the same index.
CREATE INDEX messages_recent ON messages (id DESC);
