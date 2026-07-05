# Brain CRUD tool consolidation — design

**Status:** proposed
**Date:** 2026-07-04
**Amends:** 06 (orchestrator-brain) §3, §4, §5, D3; 03 (board-mechanics) §4; 08 (user-interaction) §7

## 1. Goal

Rework the orchestrator brain's fixed tool set so tickets and feed updates each
expose a clean, **complete CRUD surface**, and the write side stops sprawling
across single-purpose lifecycle verbs. Two structural changes drive it:

1. **Consolidate ticket writes.** Five verbs — `shape_ticket`, `mark_ready`,
   `mark_blocked`, `accept_to_done`, `request_approval` — collapse into one
   `update_ticket` patch tool. `create_ticket` and a new `delete_ticket` complete C/D.
2. **Reads become pull tools, not injected context.** The full board snapshot is
   currently pushed into every pass (06 §3, D3). That is per-pass token overhead
   the model pays whether or not it needs state. Replace it with `list_tickets`
   (compact roster) + `get_ticket` (one ticket's detail), so a pass pulls only
   what it touches. The feed gains the same treatment (`list_updates`) plus the
   missing `edit_update`.

The result is one consistent shape across every read domain — **`list_*` (compact)
+ `get_*` (detail)** — matching the `list_agents` / `get_agent_updates` split
already shipped (see `2026-07-04-brain-agent-read-tools-design.md`).

### Honest accounting

Tool **count** goes 13 → 14 — this is *not* primarily a count reduction. The win
is elsewhere: the ticket **write** surface consolidates 6 verbs → 3, CRUD becomes
complete on both nouns, and per-pass context shrinks because board state is pulled
(and scoped) rather than pushed. Growth is entirely on the read side — the
deliberate price of the pull-based reads.

## 2. Current tool set (13) and the gaps

| Group | Tools | CRUD |
|---|---|---|
| Ticket writes | `create_ticket`, `shape_ticket`, `mark_ready`, `mark_blocked`, `accept_to_done`, `request_approval` | C ✓, U ✓ (5-way split), **D ✗** |
| Agent messaging | `send_to_agent` | ticket-scoped, not a field edit |
| Feed / updates | `post_update`, `retract_update` | C ✓, **U ✗**, D ✓ |
| Comms | `say` | — |
| Reads | `list_agents`, `get_agent_updates`, `bash` | — |

Concrete gaps, verified against the code and specs:

- **No ticket delete.** `board.Service` has no delete/archive op at all
  (`CreateTicket … AcceptToDone`, no `DeleteTicket`). A mistaken or duplicate
  ticket can only be dragged through the lifecycle, never removed.
- **No update edit.** The feed supports only post + retract; a wording fix means
  retract-and-repost, which churns the feed and loses the card id.
- **The brain can't read the feed.** `PassInput` injects `Board`, `Transcript`,
  `Event` — never the feed. So `retract_update(notification_id)` asks for an id
  the model has never seen. R is missing on updates.

## 3. New tool set (14)

| Domain | C | R | U | D |
|---|---|---|---|---|
| **Tickets** | `create_ticket` | `list_tickets` · `get_ticket` | `update_ticket` | `delete_ticket` (archive) |
| **Updates (feed)** | `post_update` | `list_updates` | `edit_update` | `retract_update` |
| **Agents** | — | `list_agents` · `get_agent_updates` | — | — |

Plus, unchanged in spirit: `send_to_agent` (message a ticket's agent), `say`, `bash`.

### 3.1 `update_ticket` — a facade over the board's typed operations

One patch tool. Every field routes to the board's **existing** typed method, so
board preconditions, `ErrInvalidTransition`, and the idempotency backstop (06 §6)
survive untouched — only the *tool surface* collapses.

```
update_ticket(
  id,                     // required
  title?, body?, priority?,   // → board.ShapeTicket(id, patch)
  approval_requested?,        // true → board.RequestApproval(id)
  state?,                     // "ready"  → board.MarkReady(id)
                              // "blocked"→ board.MarkBlocked(id, blocked_reason)  (reason required)
                              // "done"   → board.AcceptToDone(id)
  blocked_reason?,        // required iff state=="blocked"
)
```

**Ordering within one call.** Field edits apply **before** the state transition
(shape → approval → state), so `update_ticket(id, body=…, state="ready")` shapes
then readies in the intended order. `RequestApproval` and a non-shaping `state`
are mutually exclusive (approval is a shaping-only flag, 08 §5) — supplying both
is a malformed-arguments tool error fed back to the loop.

**Partial-failure semantics.** Each routed board call is sequential; the first
typed error stops the call and is fed back verbatim (06 §6, §8). The tool result
reports which sub-steps applied so the model can re-issue only the remainder — it
never silently half-applies without saying so.

**Not folded in:** `send_to_agent` stays its own tool. It is a *message to a
running agent*, not a ticket field edit; conflating "set fields" with "wake the
agent with this instruction" muddies both the schema and the destructive rule.

### 3.2 `delete_ticket` — archive (soft delete)

New board capability (§4). Soft delete: stamp `archived_at`, drop the ticket from
every `GetBoard` column and from `list_tickets`; the row and its history stay.

**Worker-bound rule (open sub-decision, defaulted).** A `working`/`blocked` ticket
binds a worker (03 I3). Default proposed here: archiving is allowed from any state;
archiving a worker-bound ticket **releases and recycles the worker** (same effect
as `accept_to_done`) and is therefore **destructive** — it falls under the
confirm-before-destructive prompt rule (§3.5) alongside `state="done"`. The
conservative alternative (only `shaping`/`ready`/`done` archivable; active tickets
must be accepted first) is called out in §8 for the spec-review pass.

### 3.3 Board reads replace injection — `list_tickets` + `get_ticket`

`PassInput` no longer carries `Board`. The model pulls state:

- `list_tickets()` → compact roster of **every** non-archived ticket, render order
  preserved (03 §4): `id, title, state, priority, worker_id, blocked_reason?`,
  grouped by column, with `worker_total` / `worker_free`. **No `body`** — that is
  the token saver.
- `get_ticket(id)` → one ticket in full, including `body`.

Most passes read the cheap roster and pull a full `body` only for the ticket(s)
they act on. This is where the per-pass overhead actually drops; a single
`get_board`-style full dump would move the same tokens into a tool result and add
a round-trip for no saving.

**Read-before-decide (the guarantee we're trading away).** Injection (06 D3)
guaranteed the model *cannot* act on unseen state. Pulling reintroduces the risk
of acting on a stale mental model. Two mitigations:

1. **Prompt mandate** — the system prompt requires a `list_tickets` (and
   `get_ticket` where a body matters) read before any mutating tool call in a
   pass. Golden tests pin that the read precedes the write.
2. **Board preconditions remain the backstop** — even if the model acts blind,
   the board's typed `ErrInvalidTransition` / `ErrNotFound` reject the stale
   action and feed back verbatim; the idempotency design (06 §6 — no dedupe
   table, "treat `ErrInvalidTransition` as already done") is unchanged, since
   "fresh state" is still available on demand.

**Pass-loop budget.** A read now consumes a round. Raise the bounded-loop cap
(06 §5) from 8 to **12** to absorb a read (or two) plus the same write budget as
today. (Exact value confirmed against golden fixtures during implementation.)

### 3.4 Feed reads + edit — `list_updates`, `edit_update`

- `list_updates()` → the active (non-retracted) feed cards the brain can act on:
  `notification_id, kind, body, ticket?, image_url?, created_at`. Feed cards are
  small, so `list_updates` carries full content — **no** separate `get_update`
  detail tool is needed (the one place the `list`+`get` symmetry doesn't earn its
  keep). This closes the R gap and gives `retract_update` / `edit_update` a real
  source for `notification_id`.
- `edit_update(notification_id, body?, image_url?)` → amend a posted card in
  place. `kind` is recomputed from `image_url` presence (matching `post_update`'s
  update/preview rule, 08 §7). Empty resulting `body` is rejected like
  `post_update`'s (the existing `requireField` guard).

### 3.5 Destructive-action salience

Today `accept_to_done` is its own always-destructive verb (06 §7) and the
confirm-before-destructive prompt rule keys on it by name. After consolidation the
destructive actions are **`update_ticket` with `state="done"`**, **work-discarding
`send_to_agent`**, and **`delete_ticket` on a worker-bound ticket** (§3.2). The
system prompt reprompt must re-anchor the confirm rule on these value-level and
tool-level cases; golden tests pin both an ambiguous case (asks via `say`, ends
the pass) and an unambiguous command (executes immediately), exactly as 06 §7 / §9
require today.

## 4. Cross-module changes

CRUD-complete + pull-reads necessarily crosses three modules — this is not a
brain-only change.

### 4.1 board (03 §4)

- `ArchiveTicket(ctx, id) (Ticket, error)` — sets `archived_at`; releases the
  worker when the ticket was active (per §3.2 default). New typed precondition
  errors surface verbatim like the others.
- `GetTicket(ctx, id) (Ticket, error)` — single-ticket read backing `get_ticket`.
- Migration: `archived_at timestamptz null` on `tickets`; every read path
  (`GetBoard`, the pull's candidate scan, `GetTicket`) filters `archived_at IS NULL`.
- Invariants doc: archived tickets are absent from all render columns and are not
  pull candidates.

### 4.2 runtime / feed (08 §7)

The brain's `NotificationStore` port gains two methods (satisfied structurally by
`*runtime.Service`, as today — no adapter):

```go
type NotificationStore interface {
    PostNotification(ctx, kind, body string, ticketID, imageURL *string) error
    RetractNotification(ctx, id int64) error
    ListNotifications(ctx) ([]Notification, error)          // new → list_updates
    EditNotification(ctx, id int64, body string, imageURL *string) error // new → edit_update
}
```

`EditNotification` appends `feed.updated` transactionally (same outbox discipline
as post/retract, 08 §7). `ListNotifications` returns active cards only.

### 4.3 brain (06)

- `PassInput` drops `Board`; context assembly stops calling `BoardReader.GetBoard`
  per pass. `BoardReader` is repurposed to back the `list_tickets` / `get_ticket`
  tools (adds `GetTicket`), and is now exposed *as tools* rather than injected.
- `tools.go`: remove `shape_ticket`, `mark_ready`, `mark_blocked`, `accept_to_done`,
  `request_approval`; add `update_ticket`, `delete_ticket`, `list_tickets`,
  `get_ticket`, `list_updates`, `edit_update`. `send_to_agent`, `say`, `post_update`,
  `retract_update`, `list_agents`, `get_agent_updates`, `bash` unchanged.
- `update_ticket` dispatch is the one handler with real branching (field routing +
  ordering + partial-failure reporting); everything else stays a thin
  parse → port-call → `ticketResult`.
- System-prompt reprompt: read-before-decide, the CRUD verbs, destructive salience.
  Prompt change rides the golden-test gate (06 D7); `TestSystemPrompt_*` continues
  to pin tool-**name** presence, never prose.

## 5. Idempotency & correctness (unchanged guarantees)

- **No dedupe table (06 §6).** Still holds: fresh state is pullable on demand, the
  board's strict preconditions reject stale/duplicate transitions, and the prompt
  rule "treat `ErrInvalidTransition` as already done, never retry" is preserved.
- **Tool errors → loop (06 §5, §8).** `update_ticket`'s per-field routing feeds any
  typed board error back verbatim; malformed argument shapes (e.g. `state="blocked"`
  with no `blocked_reason`, or `approval_requested` + `state`) are malformed-tool
  results that trigger the one-reprompt-then-fail path.

## 6. Testing (06 §9 golden-decision suite is primary)

- **Brain golden tests** rewrite from `board+event → tools` to
  `event → [list_tickets, (get_ticket…), …writes]`: scripted LLM over fake
  board/say/notification/inspector ports; assert the read precedes any write, and
  that `update_ticket` routes each field to the right board call. No real
  Postgres/LLM.
- **`update_ticket` routing unit tests**: each field → its board method; ordering
  (shape before transition); mutual-exclusion error (approval + state);
  partial-failure reporting.
- **board**: `ArchiveTicket` state/worker matrix (archive from each state; active
  archive releases the worker; archived ticket vanishes from `GetBoard`, `GetTicket`,
  and pull candidates). `GetTicket` found / not-found.
- **runtime**: `ListNotifications` (active-only) and `EditNotification` (kind recompute,
  `feed.updated` emitted, empty-body rejected).
- **No `/schema` regen** — these are internal LLM tools, not the client↔server wire
  contract. (`archived_at` is server-internal; confirm the board wire snapshot in
  `/schema` is unaffected because archived tickets are simply absent.)

## 7. Files (anticipated)

- `internal/brain/tools.go` — tool defs + dispatch (the bulk).
- `internal/brain/ports.go` — `BoardReader` gains `GetTicket`; `NotificationStore`
  gains `ListNotifications` / `EditNotification`; `BoardAPI` gains `ArchiveTicket`.
- `internal/brain/types.go` — `PassInput` drops `Board`; compact-roster shape.
- `internal/brain/service.go` — stop injecting the board; raise the loop cap.
- `internal/brain/prompt.go` — reprompt (read-first, CRUD verbs, destructive salience).
- `internal/brain/*_test.go` — rewritten golden + routing tests.
- `internal/board/service.go` + entities + migration — `ArchiveTicket`, `GetTicket`,
  `archived_at`, read-path filtering.
- `internal/runtime/…` — `ListNotifications`, `EditNotification` (+ outbox).
- `cmd/kiln/wiring.go` — no new wiring expected (same structural satisfiers); confirm.

## 8. Out of scope / open sub-decisions for spec review

- **`get_update` detail tool** — dropped; feed cards are small, `list_updates`
  carries full content.
- **Scoped `list_tickets` filters** (by column/state) — not in v1; the roster is
  cheap enough. Revisit if boards grow large.
- **Hard delete** — rejected in favor of archive (reversible, preserves audit).
- **Worker-bound archive rule** (§3.2) — defaulted to "archive releases the worker,
  destructive." Confirm during spec review vs the conservative "must accept first."
- **Loop-cap value** (8 → 12) — a starting point; tune against real golden fixtures.
