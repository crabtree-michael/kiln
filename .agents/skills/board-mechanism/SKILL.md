---
name: board-mechanism
description: Use when working in the board module — the authoritative state of one project's board (tickets, workers, outbox emissions) and the mechanical rules over it (invariants, the deterministic pull, transactional-outbox side effects). Backend anchor internal/board. Spec docs/specs/03-board-mechanics.md.
---

# Board mechanism (mechanics decided by spec 03)

## What it is

The authoritative state of one project's board plus the mechanical rules that govern it.
**Nothing outside the module writes these tables** (I8). Mechanics are fully specified in
`docs/specs/03-board-mechanics.md`; the product rules they realize are `01` §5 and must not
be re-opened here. Every contract is in the package's doc comments — read those for shapes;
this skill carries the *why* and the traps.

**Five states, and only five** — `shaping | ready | working | blocked | done`. Column and
zone are **derived render groupings, not stored fields** (D1). A worker row is a capacity
slot (01 §5's "sandbox", made provider-neutral per 05 A2), not a live resource handle: N rows
seeded from config *are* the WIP cap, and free vs busy is derived — busy iff an active
(`working`/`blocked`) ticket references it (D2), refined by health below.

**Board API (03 §4)** — the only mutation surface, all **named transition operations** (no
generic move, D4). Preconditions are strict: invalid or repeated transitions are typed errors
(`ErrNotFound`, `ErrInvalidTransition`), never no-ops (D8). Every mutation returns the updated
Ticket and emits `board.updated`. An archived ticket is `ErrNotFound` to every later
read/op. `SeedTicket` sits outside the API — dev/e2e only (08 §B.6), never reachable by the
real client.

## One precondition table (`transitions.go`)

Which states each operation accepts lives in exactly one place — `statePreconditions`, keyed
by `Operation` (whose values *are* the names `ErrInvalidTransition.Attempted` reports). The
guards read it, and `State.AllowedOps()` derives the reverse view — "what can be done to a
ticket sitting here" — which the brain renders onto `get_ticket`/`list_tickets` so the model
stops discovering preconditions by failed call.

**Add a state-gated operation by adding its row; never re-inline a `t.State != …` check**, or
the advertised set starts lying. State is not the whole precondition everywhere: the
worker-bound ops also need the binding, `ReassignSandbox` a free slot, `AcceptToDone` an
unspent commit — so an allowed op can still fail.

## The three sandbox controls, and the one seam they share

**`KeepSandbox`** (a *setting*, not a transition — no precondition, legal in any state, emits
only `board.updated`) is the user's "save this ticket's sandbox" choice. Its mechanical effect
is one **swap**: the `agent.release` a ticket owes when it gives up its worker —
`AcceptToDone`, and `ArchiveTicket` on a blocked ticket — becomes **`agent.snapshot`**, which
has the agent module capture that workspace as a reusable base image and (in the composition
root) points the project at it. Exactly one of the two is emitted: a workspace being captured
must never also be recycled. Everything else is unchanged: the binding still clears and
`pull.evaluate` still fires, so the *slot* is freed; only the fate of the sandbox behind it
differs. **`sandboxExitEmissions` is the single seam** both exits go through. Nothing happens
when the option is *set* — it is a standing instruction for when the ticket is done.

The capture used to be missing: the option only suppressed the release, so the box lingered
but nothing reached the provider's snapshot catalog and the workspace still died with the
sandbox (ticket 0549b739). Preserve the property that the option produces a durable artefact,
not a stay of execution.

**`KillSandbox` / `ReassignSandbox`** are the user's direct escape from a wedged workspace.
Both act on the sandbox behind a slot, not on the ticket's place on the board, and both
require `state ∈ {working, blocked}` with a bound worker.

- `KillSandbox` emits `agent.release` for the ticket's own worker and changes **nothing** on
  the ticket. The slot recreates a fresh sandbox; no work is sent, so the ticket sits
  unbriefed until poked or reassigned.
- `ReassignSandbox` locks a free worker (so an errored slot is skipped and the ticket's own
  busy slot can never come back), rebinds, and emits `agent.release` for the **old** slot plus
  `agent.send` on the new one. Result is `working` with `blocked_reason` cleared, exactly as
  `SendToAgent` leaves things. **No `pull.evaluate`** — one slot is vacated and one taken, so
  free capacity is unchanged.

**Both deliberately ignore `KeepSandbox`** — the one place `sandboxExitEmissions` is bypassed.
The option means "capture this when the work is done, don't recycle it behind my back"; these
are the user in front of it asking for the recycle now, and a saved sandbox is exactly the case
where a silent no-op — or baking a corrupted tree into a base image — would be worst.
**If you add a third exit from Developing, route it through `sandboxExitEmissions`; if you add
another override, don't.**

## Deterministic pull (03 §5)

Ready→Working happens **only** via `RunPull`, never by brain action (I6) — it is not in the
brain's tool set. Triggered by transactional `pull.evaluate` outbox entries; idempotent, so
at-least-once drain and duplicate triggers are safe. Race-free via `FOR UPDATE SKIP LOCKED`
on both ticket and worker rows, with the partial unique index `one_active_ticket_per_worker`
(I2) as the DB backstop. Order: `priority DESC, ready_at ASC, id ASC` (D9).

**Health-aware.** A Ready ticket binds **only to a healthy worker**: both the free-worker
picker and the free-slot count filter `health = 'ok'`. Health is reconciled out-of-band by the
agent-liveness reconciler via `SetWorkerHealth`, so an errored sandbox stops receiving pulls
until it recovers. D2's "free vs busy is derived" is therefore refined: a slot must be both
un-referenced **and** `health='ok'` to be pullable or counted free.

## Concurrency and side effects

One operation = one short READ COMMITTED transaction. **Lock-then-check**: `SELECT … FOR
UPDATE` the target ticket, then verify the precondition on the locked row. `SKIP LOCKED` only
in the pull; targeted operations conflict loudly. Database constraints back up every invariant
even if service code is wrong.

Side effects go through a **transactional outbox**: emissions are recorded atomically with the
state change and executed after commit by the runtime's drain loop, at-least-once with the
outbox `id` as idempotency key. Payloads are emit-time snapshots. This is distinct from the
brain-waking event queue. An effect failure never rolls back the board; exhausted `agent.send`
retries → the runtime calls `MarkBlocked` with the failure as reason.

**No agent-runtime port.** The board appends outbox intent rows; the runtime's drain loop
invokes the agent-runtime module (D5, superseding 02's topology sketch). The board's only
infrastructure dependency is Postgres.

## Persistence notes worth knowing

`text + CHECK` for `state`, not a native enum (D6). CHECK constraints enforce I1/I3/I4; the
partial unique index enforces I2. **I3 binds only LIVE rows** (`archived_at IS NULL`): an
archived row may be blocked with a NULL `worker_id`, because deleting a blocked ticket
releases the worker while keeping `state=blocked` as history. I4 still binds every row.
Changing capacity = inserting or deleting worker rows; `ReconcileWorkers` at startup **grows
or shrinks** the pool to match config (a shrink deletes only free slots).

**Migrations are embedded and ledger-tracked** — `go:embed`ed per package, applied at startup
and in tests through the per-file `schema_migrations` ledger (`testutil.ApplyMigrations`,
keyed by `postgres.MigrationsKey`). Adding a `.sql` file is enough; it reaches an existing
`kiln_test` on the next `make check`. See `local-environment` for the failure mode this
replaced.

## Testing

Unit: transition rules and error paths against an in-memory store fake — **asserting emitted
outbox rows *is* asserting side effects**, so no agent-runtime fake is needed. Integration:
real Postgres for the constraint backstops (I1–I4) and a parallel `RunPull` hammer test
proving no double-binding.

## Common footguns

- **Adding an outbox topic without widening the topic CHECK constraint.** The `outbox.topic`
  column has a `CHECK (topic IN (...))`; a new topic needs a migration to widen it or every
  transition that emits it fails the CHECK at commit. This has bitten twice — `0006`'s header
  records that leaving out `feed.completion` made "every 'done' transition fail the CHECK."
  `0014` widened it for `agent.snapshot`; the guard against a third time is that the
  keep-sandbox integration test reads its `agent.snapshot` row back out of Postgres, so a
  missing widening fails there rather than in production.
- Re-inlining a state check instead of adding a `statePreconditions` row (see above).

## Potential gotchas

- **`state_changed_at` vs `updated_at`.** A Working→Working nudge (`SendToAgent`) bumps
  `updated_at` but must **not** advance `state_changed_at` — that column is the "time in
  status" clock, so only a real state change touches it.
- **A dependency edge is not a state.** `ticket_dependencies` says a ticket waits for others,
  and its ONLY mechanical effect is that the pull skips a Ready ticket with an unmet
  dependency — it keeps its place in the pull order and holds no worker. There are still
  exactly five states (D1): "waiting" is derived on read, never stored.
- **An ARCHIVED dependency stops counting, deliberately.** Every dependency query carries
  `archived_at IS NULL`. An archived ticket can never reach done, so honouring an edge to one
  would strand its dependents forever — deleting a ticket therefore silently releases whatever
  waited on it. `ArchiveTicket` asks for dependents first and emits `pull.evaluate` when the
  answer is yes: the pullable set changed without any ticket changing state, and nothing else
  would notice. The same rule makes the cycle walk skip archived tickets — a ring that only
  closes through one is not a real cycle.
- **The cycle check runs under both tickets' row locks.** `AddDependency` locks the ticket AND
  the dependency, then walks the path. Drop either lock and two concurrent adds can each see
  an acyclic graph and commit edges that close a ring — which nothing downstream would detect,
  because the pull just skips every ticket in it forever.
- **`done_commit` uniqueness is lock-then-check.** One commit links to one ticket, enforced by
  a partial unique index plus a check under the target ticket's row lock. Skip the lock and
  two concurrent accepts can race the same commit onto two tickets.
