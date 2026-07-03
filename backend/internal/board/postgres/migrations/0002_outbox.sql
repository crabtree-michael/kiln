-- The outbox — one of the runtime's two durable queues (02 §2; DDL from 04 §2).
-- Shared migration: the board owns id/topic/payload/created_at and the emission
-- contract (03 §2.4); the delivery-state columns and everything that touches
-- them are the runtime's (04 §3).

CREATE TABLE outbox (
  id         bigserial PRIMARY KEY,  -- doubles as the idempotency key (03 §7)
  topic      text  NOT NULL CHECK (topic IN
             ('agent.send','agent.release','notify.send','pull.evaluate','board.updated')),
  payload    jsonb NOT NULL,         -- emit-time snapshot; '{}' for signal-only topics
  created_at timestamptz NOT NULL DEFAULT now(),

  -- delivery state (runtime-owned, 04 §2–§3)
  status          text NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending','done','dead')),
  attempts        int NOT NULL DEFAULT 0,
  next_attempt_at timestamptz NOT NULL DEFAULT now(),
  last_error      text,
  processed_at    timestamptz
);

CREATE INDEX outbox_due ON outbox (id) WHERE status = 'pending';
