-- The event queue — the brain-waking half of the two durable queues
-- (02 §2; DDL from 04 §2). The outbox lives in the board's shared migration
-- (backend/internal/board/postgres/migrations/0002_outbox.sql); its
-- delivery-state columns follow this same shape.

CREATE TABLE events (
  id         bigserial PRIMARY KEY,
  type       text  NOT NULL CHECK (type IN ('agent.turn_completed','human.voice_input')),
  payload    jsonb NOT NULL,          -- shape owned by the emitter's spec (02 §8 / §9)
  created_at timestamptz NOT NULL DEFAULT now(),

  -- delivery state (04 §2–§3)
  status          text NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending','done','dead')),
  attempts        int NOT NULL DEFAULT 0,
  next_attempt_at timestamptz NOT NULL DEFAULT now(),
  last_error      text,
  processed_at    timestamptz
);

CREATE INDEX events_due ON events (id) WHERE status = 'pending';
