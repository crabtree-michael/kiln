-- worker health: the pull binds Ready tickets only to healthy sandboxes.
-- 'ok' by default so a freshly-reconciled worker is pullable before its first
-- liveness observation — absence of evidence is not a failure. The agent
-- liveness reconciler flips a worker to 'errored' when its sandbox is in a
-- terminal failure state (05 §6) and back to 'ok' on recovery, via
-- board.Service.SetWorkerHealth. FreeWorker and the WorkerFree count both
-- filter health = 'ok' (03 §5 amended).
-- Migrations apply in filename order (02 §14).

ALTER TABLE workers ADD COLUMN health text NOT NULL DEFAULT 'ok'
  CHECK (health IN ('ok', 'errored'));
