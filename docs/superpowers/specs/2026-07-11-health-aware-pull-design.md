# Health-aware pull — design

**Date:** 2026-07-11
**Status:** Implemented (commits `bd08969`, `fab4bd2`)

## Problem

Sandboxes can fail to provision (a "sandbox health" failure). The board's
deterministic pull (`board.Service.RunPull` → `pullOnce` → `FreeWorker`) is
**health-blind**: the `workers` table records only worker identity and ticket
bindings, so `FreeWorker` will happily bind a Ready ticket to a worker whose
sandbox is errored. The `agent.send` work order then goes to a dead sandbox and
the ticket stalls in Working.

An earlier change (commit `3499b6d`) surfaced the failure two ways:
- a persistent **error band** on the dock ("X of M sandboxes failing"), and
- a **`list_agents` health warning** telling the brain the failure is
  infrastructure.

The brain guidance was subsequently corrected so it no longer halts *all* ticket
work on the first failure, but instead scales to the healthy capacity. That is a
soft guarantee — it relies on the LLM honoring the guidance.

This design adds the **hard guarantee**: the pull itself must only bind Ready
tickets to healthy sandboxes, so that at most `healthy-free-worker-count`
tickets ever start, regardless of what the brain does.

## Approach

Persist worker health on the board's `workers` table and filter the pull on it.
Health is *detected* by the agent module (which already polls provider liveness)
and *written* by the board module (which owns the row). The agent module supplies
neutral facts through an existing outbound port; it never touches board SQL.

Rejected alternative — a point-in-time exclude-set passed into `RunPull` — was
lighter (no migration) but only best-effort and threaded provider health through
the runtime on every pull. The persisted column is atomic at the pull query and
reuses the liveness reconciler that already exists.

## 1. Data model

One migration on the board's `workers` table:

```sql
ALTER TABLE workers ADD COLUMN health text NOT NULL DEFAULT 'ok'
  CHECK (health IN ('ok','errored'));
```

- `ok` default: a freshly-seeded worker is pullable *before* its first health
  observation. Absence of evidence is not a failure.
- Only two values today (`ok` / `errored`), matching the single unhealthy state
  the rest of the system recognizes (`AgentErrored`). `stopped`/`starting`/`idle`
  /`building` are all healthy-or-transient and count as `ok` — a `stopped`
  worker is auto-stopped and woken on demand, so it remains pullable.
- The board is the **sole writer** of the row, so ownership stays single-module.

## 2. Sync path

Reuse the existing liveness reconciler. `agent.Service.refreshStatuses` already:
1. polls `ListAgents` per project each tick (reads live provider health),
2. diffs against `lastStatus` (`statusChanged`),
3. on change calls `BoardRefresher.RefreshBoard` to re-push Streams.

Extend the `BoardRefresher` outbound port (agent module → client-facing layer)
with one method the agent calls on the same change tick:

```go
// agent.BoardRefresher
SetWorkerHealth(ctx context.Context, projectID string, erroredWorkerIDs []string) error
```

The agent module passes the set of worker IDs it currently observes as
`AgentErrored` for that project. The `cmd/kiln` adapter maps this to a new
`board.Service.SetWorkerHealth` → store method that reconciles the **whole
project** in one statement:

```sql
UPDATE workers
SET health = CASE WHEN id = ANY($2) THEN 'errored' ELSE 'ok' END
WHERE project_id = $1;
```

Full reconcile (not incremental "set errored") means recovery and
provider-dropped workers flip back to `ok` automatically — no stuck-errored
rows.

**ID alignment:** the pull binds board `worker.ID`; `agent.send` carries it; the
provider names the worker `prefix + id`; `ListAgents` trims the prefix back to
the same id. So the errored ID set the agent reports keys directly on
`workers.id`. Workers not present in the live provider list (e.g. never started)
simply aren't in the errored set and stay `ok`.

**When to write:** inside the existing per-project loop in `refreshStatuses`,
where `pid` and that project's `got []AgentInfo` are in scope. Derive the
project's `errored` worker IDs from `got` and call
`SetWorkerHealth(ctx, pid, errored)` **each tick, per project** — not gated on
the global `statusChanged` flag. The reconcile UPDATE is idempotent and a single
indexed write per project, and correctness must not depend on the Streams-push
change gate (which is aggregated across all projects and exists only to decide
whether to re-push Streams). The reconciler already makes one `ListWorkers` call
per project per tick, so this adds no provider round-trips. The existing global
`statusChanged`-gated `RefreshBoard` nudge is left exactly as-is.

Note the errored set must be built **per project inside the loop** — the
existing flattened `infos` slice (aggregated across projects for the global diff)
cannot be used, because `SetWorkerHealth` needs each project's own worker IDs.

## 3. Pull enforcement

No pull signature changes — the column does the work:

- `FreeWorker`'s candidate scan (`lockFreeCandidates` in
  `board/postgres/store.go`) adds `AND s.health = 'ok'`. An errored worker is
  never returned as free, so the pull never binds a ticket to it.
- `Snapshot`'s `WorkerFree` count adds the same `AND s.health = 'ok'`, so the
  brain's `workers: N free / M total` line and the pull agree on real capacity.
  `WorkerTotal` stays the physical worker count, pairing with the banner's
  "X of M sandboxes failing".

Result: the pull binds at most `count(free workers WHERE health = 'ok')` tickets
— exactly the number of available sandboxes.

## 4. Failure isolation

- **Health read fails mid-reconcile:** `refreshStatuses` already logs-and-skips
  that project, so it just doesn't update health that tick — stale-but-safe,
  never freezes the board.
- **`SetWorkerHealth` write fails:** logged, retried on the next change tick; the
  column lags at most one tick.
- **Staleness window:** health is as fresh as the reconciler tick — the same
  freshness that already drives the Streams liveness push, so it is consistent
  with the rest of the system. The brain's `list_agents` guidance and the
  dead-letter → Blocked path remain as backstops for the sub-tick race (a worker
  erroring between the reconcile and the bind).
- **`refresher` is nil** (test / single-purpose wiring): `SetWorkerHealth` is
  skipped along with `RefreshBoard`, exactly as today; health stays at its
  `ok` default and the pull behaves as it does pre-change.

## 5. Testing

Three levels, per the end-to-end-development hard gate:

- **Store (`board/postgres`):**
  - `FreeWorker` skips an `errored` worker and returns the `ok` one.
  - `Snapshot.WorkerFree` excludes errored workers; `WorkerTotal` does not.
  - `SetWorkerHealth` flips health in both directions (errored set → errored;
    absent from set → back to ok).
- **Agent unit (`internal/agent`):** `refreshStatuses` calls `SetWorkerHealth`
  per project with the correct errored-ID set (a fake `BoardRefresher` captures
  and asserts the payload); a project with an errored worker reports that id, a
  healthy project reports an empty set. Multi-project: each project gets its own
  errored set, never another project's ids.
- **Board service (`internal/board`):** a pull with one `errored` and one `ok`
  free worker binds exactly one ticket, to the `ok` worker — not two.

## Out of scope

- No new health states beyond `ok`/`errored`.
- No change to the banner / API alert derivation (already correct).
- No change to the brain `list_agents` guidance (already corrected); it now
  serves as human-readable context and a backstop, not the enforcement point.
