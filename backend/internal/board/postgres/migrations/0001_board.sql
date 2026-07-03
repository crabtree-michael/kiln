-- Board entities: docs/specs/03-board-mechanics.md §8 (definitive DDL).
-- Migrations apply in filename order; tooling TBD (02 §14).

CREATE TABLE workers (
  id         uuid PRIMARY KEY,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE tickets (
  id             uuid PRIMARY KEY,
  title          text NOT NULL CHECK (title <> ''),
  body           text NOT NULL DEFAULT '',
  state          text NOT NULL DEFAULT 'shaping'
                 CHECK (state IN ('shaping','ready','working','blocked','done')),
  priority       int  NOT NULL DEFAULT 0,
  worker_id      uuid REFERENCES workers(id),
  blocked_reason text,
  ready_at       timestamptz,
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  CHECK ((state IN ('working','blocked')) = (worker_id IS NOT NULL)),       -- I3
  CHECK ((state = 'blocked') = (blocked_reason IS NOT NULL))                -- I4
);

-- I2: the WIP cap — at most one active ticket per worker
CREATE UNIQUE INDEX one_active_ticket_per_worker
  ON tickets (worker_id) WHERE state IN ('working','blocked');

CREATE INDEX tickets_ready_pull_order
  ON tickets (priority DESC, ready_at ASC, id ASC) WHERE state = 'ready';
