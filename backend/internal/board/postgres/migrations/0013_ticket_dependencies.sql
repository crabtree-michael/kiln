-- ticket_dependencies: "this ticket cannot start until those tickets are done".
--
-- An edge (ticket_id -> depends_on_id) means ticket_id WAITS FOR depends_on_id.
-- Its whole mechanical effect is on the pull: a Ready ticket with an unmet
-- dependency is skipped by NextReadyTicket, so it holds its place in the queue
-- without consuming a worker slot. Nothing else in the board reads it — the
-- five states are unchanged, and "waiting on a dependency" is derived at read
-- time (a Ready ticket with unmet edges), never a sixth stored state (03 D1).
--
-- A separate table rather than an array column on tickets: the pull's skip test
-- and the cycle check are both joins over the edge set, and one edge is exactly
-- one row to insert or delete.
--
-- ON DELETE CASCADE covers the hard-delete case, but the board soft-deletes
-- (archived_at), so the queries carry the real rule: an edge to an ARCHIVED
-- ticket is ignored everywhere — it can never reach done, so honouring it would
-- strand its dependents forever. Archiving a ticket therefore silently releases
-- whatever was waiting on it, and the edge rows stay for history.
--
-- Migrations apply in filename order (02 §14).

CREATE TABLE ticket_dependencies (
  project_id    uuid NOT NULL,
  ticket_id     uuid NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  depends_on_id uuid NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  created_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (ticket_id, depends_on_id),
  -- A ticket waiting on itself is unsatisfiable by construction; the service
  -- refuses it as the length-zero cycle, and this is the DB backstop.
  CONSTRAINT ticket_dependency_not_self CHECK (ticket_id <> depends_on_id)
);

-- The pull's skip test walks edges forwards (given a ticket, are any of its
-- dependencies unmet?); the PRIMARY KEY already serves that. The cycle check
-- and "did archiving this strand anyone?" walk them backwards, hence this one.
CREATE INDEX ticket_dependencies_depends_on ON ticket_dependencies (depends_on_id);

-- Every read is tenant-scoped (11 §3).
CREATE INDEX ticket_dependencies_project ON ticket_dependencies (project_id, ticket_id);
