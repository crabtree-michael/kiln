-- agent_turns — the agent module's only state (05 §7): one turn state
-- machine per in-flight Send/Release, keyed by the outbox id. This is the
-- idempotency dedupe the provider doesn't give us, and the recovery source
-- (on start, continue every non-terminal row). Adapter-layer state, never
-- board state (03 I8): no board table references, no board invariants.

CREATE TABLE agent_turns (
  idempotency_key bigint PRIMARY KEY,   -- the outbox id (04 §3)
  kind       text NOT NULL CHECK (kind IN ('send','release')),
  ticket_id  uuid,                      -- NULL for release operations
  worker_id  uuid NOT NULL,             -- the board worker-slot uuid (03 §2.3); by value, never a FK
  message    text NOT NULL DEFAULT '',  -- what StartTurn sends; recovery must start a never-started turn (05 §7)
  phase      text NOT NULL DEFAULT 'recorded'
             CHECK (phase IN ('recorded','worker_ready','turn_started','done','failed')),

  -- opaque provider handles as they become known (05 §6–§7);
  -- for Amika: sandbox id, and {session id, job id} respectively
  provider_worker text,
  provider_turn   jsonb,

  -- the machine's own retry bookkeeping (05 §5)
  attempts   int NOT NULL DEFAULT 0,
  last_error text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

-- The poller's working set (05 §5). 'failed' is not at rest — it still owes
-- the error-shaped agent.turn_completed event before moving to done.
CREATE INDEX agent_turns_open ON agent_turns (idempotency_key)
  WHERE phase <> 'done';

-- LatestForWorker: newest operation per slot decides
-- first-message-vs-continuation (05 §2.1, §3). Outbox ids are monotonic.
CREATE INDEX agent_turns_by_worker ON agent_turns (worker_id, idempotency_key DESC);
