# Kiln — Architecture Audit & Code Review

**Date:** 2026-07-08
**Scope:** Full codebase — Go backend (`/backend`, ~15.9k LOC non-test), React/TS frontend (`/frontend`, ~9.2k LOC), wire contract (`/schema`), and the test/CI harness.
**Method:** Structural review of module topology, layering, coupling, the two-queue design, multi-tenancy, the wire contract, and the test gate. Findings below were read in-source and the highest-severity ones independently verified.

---

## 1. Executive summary

Kiln is an **unusually disciplined codebase**. The architecture the spec describes (a modular monolith with hard, port-mediated module seams — `docs/specs/02` §2) is genuinely realized: modules do not import each other's behaviour, everything is wired through adapters at a single composition root, ports are backed by hand-written in-memory fakes, and the type/lint escape hatches are effectively banned and *actually absent* (zero `any`/`as`/`@ts-ignore` in frontend production code; zero `TODO`/`FIXME`/`HACK` across the whole tree; narrowly-scoped `//nolint` only). This is the top ~5% of codebases for boundary hygiene.

The problems are therefore **not sloppiness — they are structural drift and a few load-bearing correctness gaps**:

1. **The spec says "single project, single user"; the code is fully multi-tenant.** Identity/OAuth, a per-project provider registry, and `project_id` threaded through every table and adapter have been *retrofitted* onto a single-tenant design. Tenant isolation is now enforced **by convention (caller discipline), not by structure** — the type system would not stop a cross-tenant leak.
2. **The two-queue (events + outbox) transactional-outbox pattern is implemented correctly on the board/notification side but *not* on the agent→events side** — a real at-least-once/duplicate-delivery gap with no idempotency key.
3. **The composition root and a few core services have grown large** (adapters.go 830 LOC / 21 adapters, wiring.go 746 LOC, runtime service with 14 ports, brain/tools.go 931 LOC), with fragile late-binding construction cycles.
4. **The "hard gate is a wall" promise is partly aspirational**: CI does not block deploys, and the schema-drift guard exists but is unwired.

Because the app is explicitly **not in production and has no users** (`AGENTS.md`), none of these are live incidents. But they are the structural debt that will bite first when Kiln ships, and several are cheap to fix now.

### Priority matrix

| # | Finding | Area | Severity | Effort |
|---|---------|------|----------|--------|
| P1 | Agent turn→event emit is non-transactional; events queue has no idempotency key → duplicate brain passes | Backend / two-queue | **Critical** | Medium |
| P2 | Tenant isolation is by-convention, not structural (unscoped-by-id reads, global `thinking` flag, global concurrency cap) | Multi-tenancy | **High** | Medium–High |
| P3 | Tenant registry: `Invalidate` TOCTOU vs in-flight build + no `Providers` teardown (repo-clone leak) + unbounded map growth | Multi-tenancy | **High** | Low–Medium |
| P4 | CI is advisory, not a wall (Render deploys red `main`); `schema-verify` drift guard unwired | Harness | **High** | Low |
| P5 | `board.Tx` leaky port — service owns SQL lock/transaction choreography | Backend / layering | **High** | Medium |
| P6 | God units: runtime service (14 ports), brain/tools.go (931 LOC), composition root (~1.6k LOC) | Backend | **High** | Medium |
| P7 | Frontend: authoritative merge/reconcile logic in the "thin" feed-store (523 LOC) | Frontend | **High** | Medium |
| P8 | Cross-cutting duplication (card-kind taxonomy, clamp/CSS-var effects, reconnect state machine) | Frontend | **Medium** | Low–Medium |
| P9 | Retrofitted `project_id` migrations: no FKs, nullable+backfill, blocking boot-time NOT-NULL flip | Backend / data | **Medium** | Medium |
| P10 | Spec/doc drift — 02 and several package docs describe a system that no longer exists | Docs | **Medium** | Low |

---

## 2. What is working well (keep doing this)

These are load-bearing strengths; changes should preserve them.

- **Module boundaries are real.** No domain module imports another's behaviour. The only cross-module imports are shared leaf infra (`obs`, `pgutil`), generated `wire` types, and entity vocabulary (`brain`→`board`). Everything else is wired through ports at `cmd/kiln`. This is the modular-monolith promise actually delivered.
- **Ports + hand-written fakes.** Six `fakes_test.go` are uniform in-memory implementations of real interfaces — no mock framework, no call-order coupling. The >1.25:1 test-to-code ratio is earned, not mock bloat (`docs/specs/02` §2, §4 satisfied).
- **Integration gating is 100% consistent** — every `*_integration_test.go` uses `//go:build integration` + a `t.Skip` when `TEST_DATABASE_URL` is unset, and CI supplies the DB so they actually run (`.github/workflows/check.yml:32-47`).
- **Escape-hatch hygiene is genuinely clean** — 52 narrowly-scoped `//nolint`, 0 `eslint-disable`, 0 `@ts-ignore`, no blanket suppressions.
- **The wire schema is being maintained by hand-discipline** — `openapi.yaml` and both generated files change only in the same commits; generated files carry `DO NOT EDIT` headers and are not hand-edited.
- **The outbox worker's retry/backoff/dead-letter path is correct** (`internal/runtime/worker.go`): panic→retryable, bounded backoff with overflow guard, claim's `next_attempt_at` doubles as a visibility timeout, per-project serialization via a busy-set.
- **The voice pipeline is a model of testable layering** — `commit-machine.ts` is a pure I/O-free reducer; all AudioContext/WebSocket I/O is quarantined behind a provider-neutral event seam.

---

## 3. Findings by area

Severity reflects structural importance *when Kiln ships*; nothing here is a live incident today (no production, no users).

### 3.1 The two-queue design — the core correctness gap

**[Critical] Agent turn completion is not transactional with its event emit, and the events queue has no dedup key.**
`internal/agent/service.go:593-595` (`stepCheckTurn`): `s.emitCompleted(...)` inserts the `agent.turn_completed` event in one statement, then `t.Phase = PhaseDone; s.update(...)` persists the turn in a **separate** statement. A crash between them leaves the turn non-terminal, so the poller re-runs `stepCheckTurn` and **emits the completion event again**. The events table (`internal/runtime/postgres/migrations/0001_events.sql:6-19`) has only a `bigserial` PK — no idempotency key, no unique constraint — so the duplicate is accepted and the brain runs a second pass on the same completion. This directly contradicts the module's own documented contract (`internal/agent/store.go:22-25`: "moving a machine to done must commit in the same transaction as enqueueing its `agent.turn_completed` event"), which was "left settled at implementation" and never actually settled. The **outbox** half of the two-queue design *is* transactional and idempotent (board/notification writers append `feed.updated` in the same tx; `agent_turns.idempotency_key` + `ON CONFLICT DO NOTHING`); the **events** half is not. This is the single most important structural defect. *Fix: give `agent` a transactional store (mirror `board.Tx`) so the phase update and the event insert commit together, and/or add a unique idempotency key on `events` (e.g. `(turn_id, phase)`).*

**[Medium] Idempotency is inconsistent across the two event producers.** Both `EnqueueEvent` (`internal/runtime/service.go:195`) and `emitCompleted` do bare inserts with no key. The brain absorbs replays only via board preconditions (`internal/brain/service.go:86-99`) — which covers board mutations but not non-idempotent side effects of a duplicated pass (a duplicate `say`, `post_update`, or new `send_to_agent` turn). The design is "at-least-once + idempotent handlers" on the outbox but "at-least-once + hope" on events.

**[Medium] The service layer over the transactional outbox is vestigial, and notification *policy* lives in the queue router.** The transactional invariant ("a feed mutation implies a `feed.updated` emit") is buried in the store (`internal/runtime/postgres/store.go:316-445`), while the runtime service methods that should own it are pure pass-throughs (`service.go:380-467`). Separately, product policy — *which* board changes are user-worthy, with per-verb copy — is embedded in the outbox drain handler (`feedUpdateVerbBody`, `service.go:744-764`). Domain policy is sitting in transport-routing code.

### 3.2 Multi-tenancy — correct by convention, not by structure

The single-tenant→multi-tenant retrofit is the dominant source of structural risk. Isolation currently holds because every `projectID` is server-derived, but nothing *structural* enforces that.

**[High] Isolation is enforced by caller discipline, not the type system.**
- `identity.Service.GetProject` (`service.go:298`) and `RuntimeConfig` (`:341`) take a raw `projectID` with **no owner argument** and would return any tenant's data. Safe only because the sole callers pass server-side ids from `ListProjectIDs`. An HTTP handler passing a client-controlled id would leak cross-tenant. (Request-path lookups `ProjectFor`/`Me` *are* correctly owner-scoped — `session.go:87`.)
- The hub's `thinking` spinner flag is **hub-global, not per-project** (`internal/api/hub.go:72`, read at `routes.go:579`) — project A's "brain thinking" state is visible to project B. Safe only under the v1 serial-brain assumption; a latent tenancy bug.
- `maxInFlightProjects = 4` (`internal/runtime/worker.go:38`) is a hard **global** cross-tenant concurrency cap with no per-project fairness — one project's burst can starve others. A scaling ceiling baked into a const.

**[High] Tenant registry (`internal/tenant/registry.go`) — three defects in the ~120-line tenancy pivot.**
- *Invalidate TOCTOU:* `Invalidate` is `delete(cache, id)` with no epoch/generation guard (`:119-123`). An `Invalidate` that fires *during* an in-flight build (after the re-check at `:94-99`, before the cache write at `:111`) is silently lost — the stale provider serves until the next write. Since `identity.SetInvalidator` fires this on every credential edit (`wiring.go:214`), a credential fix racing a build won't take effect. The code comment at `:93` overstates the guarantee. *Fix: capture a generation counter before build, compare-and-swap on store.*
- *Provider leak:* `Providers` holds an Amika HTTP client and an on-disk repo clone (`wiring.go:333`) but has **no `Close()`**. Every rebuild abandons the prior bundle; the clone under `RepoDir/<pid>` is orphaned on disk. N credential edits leak N-1 clones.
- *Unbounded growth:* `keyLocks` and `cache` are never pruned — a deleted project stays cached forever; no TTL/LRU. Also, build errors aren't cached (correct for recovery) but there's no backoff, so a persistently-broken project re-runs the full resolve + repo-clone on *every* incoming event.

### 3.3 Backend layering & god units

**[High] `board.Tx` is a leaky persistence port.** `internal/board/store.go:40-72` exposes `LockTicket` (`FOR UPDATE`), `NextReadyTicket`/`FreeWorker` (`FOR UPDATE SKIP LOCKED`), and `AppendOutbox` as first-class port methods, and `board.Service` is written directly against this lock-aware, transaction-scoped shape. The service *owns* the lock-then-check choreography (including the subtle READ-COMMITTED race-avoidance in `postgres/store.go:346-365`) rather than expressing intent and letting the adapter choose locking. The port is a thin veneer over SQL transactions, not a business-level persistence contract.

**[High] God units.**
- `internal/runtime/service.go` — the service depends on **14 ports** and `NewService` takes 14 positional args (`:138-188`); the doc comment even prescribes a "append-at-the-end" workaround. It is simultaneously event dispatcher, outbox router, transcript facade, feed assembler, notification CRUD facade, and push coordinator (813 LOC).
- `internal/brain/tools.go` (931 LOC) conflates tool *schemas* (`:1-368`), the *dispatch table* (`:395-462`), and every tool *handler* (`:464-931`) — with a mini state-machine (`doUpdateTicket`→…→`applyState`, `:499-654`) embedded. Split into schemas / dispatch / handlers.
- `internal/agent/service.go` (837 LOC) holds Send/Release, reconciler, poller, the full turn state machine, worker cache, and liveness differ — the whole module minus types in one file.
- `brain.NewService` — 10 positional args over 9 ports (`service.go:61-77`), same anti-pattern. *Fix for the constructor cases: options struct or a `Deps`/`Ports` struct.*

**[Medium] Cross-module entity policy is contradictory.** `brain` imports `board` for entity types (`board.Ticket`, `board.Snapshot`, etc. — a defensible shared-vocabulary choice). But `runtime` and `agent` go to lengths to **mirror board payload structs by value** to avoid the same import (`runtime/service.go:640-667`, with explicit "never imports internal/board" comments). Two contradictory rules for one concern, and the mirrored structs can silently drift out of sync with no shared round-trip test. Pick one: entities are a shared kernel (import everywhere, delete mirrors) or they aren't (mirror in brain too).

### 3.4 Composition root

**[Medium] Adapter sprawl + fragile construction cycles.** `cmd/kiln/adapters.go` (830 LOC, 21 adapters) + `wiring.go` (746 LOC) is ~1.6k LOC of wiring. Several adapters now exist only to inject/drop a `projectID` (`boardAPIAdapter`'s 8 near-identical one-liners `:189-238`; `agentRuntimeAdapter` `:145-159` exists purely to *drop* a redundant arg) — retrofit residue from the tenancy flip. Two construction cycles are broken by mutable late-binding (`agentEventAdapter.rt` assigned at `wiring.go:245`; the registry closure capturing `&rtSvc`/`&agentSvc`, `:210,285`). This works only because assignment precedes the first event — there is **no compile-time guarantee**; reordering two lines yields a nil-deref at first event, invisible to the type checker.

**[Low] Duplicate sentinel errors** — `errIdentityNotConfigured` (`wiring.go:173`) and `errIdentityUnconfigured` (`:451`) carry the identical message.

### 3.5 API module boundary

**[Medium] `api` imports concrete domain types.** `routes.go:16-19`, `hub.go:13-14`, `session.go:19` pull `board.Snapshot/Ticket/State`, `runtime.Message/FeedCard`, `identity.User/Project` directly into handlers/mappers — inconsistent with `agent`/`push`/`beta`/`voice`, which are value-mirrored behind local ports. A board type change ripples into api.

**[Medium] Business logic in transport handlers.** `handleAccept` (`routes.go:728-748`) fabricates a natural-language brain directive and does an extra board read to prettify it — orchestration policy that belongs behind an `AcceptProposal(ctx, projectID, ticketID)` port.

**[Low] Stale package doc** — `api/doc.go:7-8` still declares the package single-user, auth-deferred, local-only, flatly contradicted by the OAuth/session/tenancy surface now present. Decentralized error→status mapping (each handler hand-maps sentinels; a missed case falls to 500) and inconsistent error body shape (`text/plain` vs JSON in `session.go:90`).

### 3.6 Data / migrations

**[Medium] Retrofitted `project_id`, no referential integrity.** `board/0008`, `runtime/0007`, `agent/0002`, `steward/0002`, `push/0003` all `ADD COLUMN project_id uuid` nullable, backfilled at boot (`bootstrap.go:196-201`), with **no FK to `projects` anywhere** (deliberate "carried by value" — but zero DB-level catch for dangling ids). The NOT-NULL finalizer (`bootstrap.go:224-249`) is idempotent, but `SET NOT NULL` takes `ACCESS EXCLUSIVE` + full-table validation synchronously in the boot path on `events`/`outbox`/`notifications` — a slow blocking first boot on a large DB. No `CREATE INDEX CONCURRENTLY`, no down-migrations, missing `project_id` indexes on `agent_turns`/`steward_pokes`.

**[Low] Migration numbering has no uniqueness enforcement** — duplicate `0004_*` filenames in `runtime/postgres/migrations` (functionally safe via byte-sort order, but a smell). The ledger keys on *filename*, not content — renaming re-runs a migration; editing a body never re-runs it.

### 3.7 Frontend

**[High] Authoritative merge/reconcile logic in the "thin" client.** `stores/feed-store.tsx` (523 LOC) runs retract-reconciliation against a history-window floor (`:179-209`), keyset pagination cursors, an optimistic-accept TTL cache with reappear timers (`:296-325`), swipe-dismiss suppression with rollback (`:332-398`), and a session-frozen last-seen divider (`:170-177`). This is the largest concentration of leaked, hard-to-test state in a client the spec says holds none.

**[High] Cross-cutting duplication with no source of truth.**
- The `update|preview|poke|done` vs `blocker|proposal` **card-kind taxonomy is re-expressed inline in 6+ files** (`feed-store.tsx:47-53`, `PrimaryScreenView.tsx:96/106-116/197`, `FeedCardItem.tsx:210-230`, `App.tsx:22`, `NotificationsPanel.tsx:37`, `feed-format.ts:147`) — high drift risk.
- `useClampOverflow` (`FeedCardItem.tsx:48-70`) and `ClampedText` (`ActivityRow.tsx:37-52`) are the same overflow-measurement logic (the comment admits it "Mirrors ActivityRow's ClampedText").
- The ResizeObserver→CSS-var-on-root effect is duplicated (`Dock.tsx:152-173`, `ActivityRow.tsx:159-180`), both via fragile `closest()` DOM traversal to mutate a distant ancestor.

**[High] `PrimaryScreenView.tsx` (370 LOC) god component** — ~24 props and 5 module-level feed-logic helpers (`:95-166`) that belong in `feed-format.ts` (which exists for exactly this). `dashboard/ConfigFields.tsx` (439 LOC) triplicates ~90 lines of secret-field markup a single `<CredentialField>` would collapse (`:286-423`).

**[Medium] Pattern inconsistency.** The `createStoreContext` factory is used by half the stores; `feed`/`activity`/`voice` contexts hand-roll the exact boilerplate it exists to eliminate. The reconnect "refetch-once-on-gap" state machine is copy-pasted across 4 stores. Two competing top-level compositions (`App.tsx` `/debug` vs `PrimaryScreen.tsx`) duplicate provider bridging. Two send paths to `POST /api/message` (chat optimistic+retry vs voice raw) with divergent failure models.

**[Low] `acceptTicket` (`transport.ts:709-716`)** is the one fetch wrapper missing the `!response.ok` guard — a 4xx/5xx throws an opaque JSON-parse error instead of a clean HTTP error.

### 3.8 Harness & wire contract

**[High] CI is not a wall.** `.github/workflows/check.yml:14-16`: Render auto-deploys `main` on push regardless of the workflow — "a red run flags the break but does not block the deploy." No branch protection. The "red means you cannot land" promise (`Makefile:3`) is enforced only by opt-in local pre-push hooks (bypassable with `--no-verify`).

**[High] The schema-drift guard is defined but unwired.** `make schema-verify` exists (`Makefile:87-90`) and README/CI *claim* stale generated files fail CI, but `make check` = `lint typecheck test` (`Makefile:42`) does **not** depend on it, and it is invoked nowhere in `check.yml` or the hooks. A hand-edit or forgotten `make schema` regen passes CI silently. One-line fix, high ROI.

**[Medium] Regeneration is not reproducible** — `oapi-codegen` is `go install ...@latest` (unpinned; `Makefile:29-30`), `openapi-typescript` is caret-ranged (`frontend/package.json:45`). No `tools.go`/`go.mod` tool directive. Two developers regenerate divergent output. Combined with the unwired verify, "generated files never drift" is unenforceable in practice.

**[Medium] Contract gap: the `/auth/github/*` + `/auth/logout` browser flow** (`routes.go:430-432`, spec 11 §2) is a real client-facing contract absent from `openapi.yaml` — its redirect/cookie/logout shape lives only in hand-written Go. (The `/api/dev/*` routes are also absent but *deliberately* documented-exempt — acceptable.)

**[Low] The mandated third test level (e2e) is out of the gate** — reasonable given e2e needs live keys and is LLM-nondeterministic (`tests/README.md` owns this honestly), but a literal divergence from spec §4's "e2e as blocking hard gate." Consider a deterministic, dev-seeded, no-LLM e2e subset in CI. ~15 `time.Sleep` calls in runtime/agent tests are a latent flakiness vector (all currently paired with `Eventually`, so defensible).

---

## 4. Recommendations (prioritized)

**Do first — cheap, high leverage:**
1. **Wire `schema-verify` into `make check`** (P4) — one line; makes the source-of-truth guarantee real.
2. **Add branch protection on `main`** (P4) — turns CI from advisory into the wall the docs already claim.
3. **Pin codegen tool versions** (`tools.go` for oapi-codegen; exact `openapi-typescript`) (P4).
4. **Update the drifted docs** — `docs/specs/02` framing, `api/doc.go`, and any "single user" language — to describe the multi-tenant reality (P10). De-duplicate the identity sentinel errors.

**Do next — the real structural fixes:**
5. **Close the events-queue idempotency gap** (P1) — give `agent` a transactional store so the phase-done update and the completion-event insert commit together, and/or add a unique key on `events`. This is the one Critical correctness defect.
6. **Make tenant isolation structural** (P2/P3) — add an owner argument (or an owner-scoped type) to `identity.GetProject`/`RuntimeConfig`; make the hub `thinking` flag per-project; add an epoch guard + `Close()` + pruning to the tenant registry.
7. **Split the god units** (P6) — options/Deps struct for the 14-arg runtime and 10-arg brain constructors; split `brain/tools.go` into schemas/dispatch/handlers; consider extracting the runtime feed/notification facade.
8. **Establish single sources of truth on the frontend** (P8) — one card-kind taxonomy module, one clamp/overflow hook, one CSS-var-publish hook; route feed logic through `feed-format.ts`; route all stores through `createStoreContext`.

**Plan deliberately — larger or judgement calls:**
9. **`board.Tx` leaky port** (P5) — redesign toward intent-level persistence methods, or explicitly accept it as a pragmatic transaction seam and document the choice.
10. **Migrations** (P9) — add FKs (or document the value-carried decision), add missing `project_id` indexes, and move the NOT-NULL flip off the synchronous boot path before any real data volume exists.
11. **Reassess the composition root** (P6) — collapse the now-trivial projectID adapters and replace the pointer-to-pointer late-binding with an explicit two-phase builder that fails at construction, not first event.
12. **Pull authoritative merge logic out of `feed-store`** (P7) — decide what the server should send ordered/reconciled vs what the client legitimately owns (optimistic UI), and shrink the client to the latter.

---

*Nothing here is urgent in the "production is on fire" sense — Kiln has no users yet. The value of fixing now is that this is exactly the window where the retrofit debt (multi-tenancy, the events-queue gap, the wiring cycles) is cheapest to pay down, before real data and real tenants make each of these a migration instead of an edit.*
