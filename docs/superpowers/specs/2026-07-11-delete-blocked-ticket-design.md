# Delete a blocked ticket from the detail sheet — design

**Date:** 2026-07-11
**Status:** Proposed (design only — no implementation in this change)
**Scope:** `frontend` (the ticket-detail sheet + its primary-screen wiring),
`backend/internal/api` (the client delete endpoint), `backend/internal/brain`
(the `delete_ticket` tool contract + prompt), `backend/internal/board`
(`ArchiveTicket` + one invariant migration). Builds directly on the existing
tap-Delete affordance for proposals (the shaping-only Delete button already in
`TicketDetail.tsx`) and the archive path behind it.

## Problem

A ticket the user recognizes as a **duplicate** (or otherwise a mistake) is easy
to discard *while it is still in the backlog* — the ticket-detail sheet already
shows a **Delete** button on a shaping proposal
(`TicketDetail.tsx:280`), which routes through the brain's `delete_ticket` →
`board.ArchiveTicket`. But once that duplicate has been accepted and **pulled
into development**, it is worker-bound, and there is no way to remove it from the
client at all.

The wall is mechanical, not cosmetic. There is no state literally named a "delete
blocker"; the delete-blocked condition is an *emergent precondition failure*:

- `State.Active()` is `working || blocked` (`board/entities.go:18`) — "active"
  means the ticket binds a worker (invariant **I3**, `migrations/0001_board.sql:21`).
- `ArchiveTicket` **refuses any active ticket** —
  `if t.State.Active() { return &ErrInvalidTransition{…} }`
  (`board/service.go:462`) — because archiving it directly would either strand or
  silently release the bound worker. Its comment says the ticket "must be resolved
  first."
- But the *only* sanctioned exit from an active state is `AcceptToDone`
  (working|blocked → done, `board/service.go:277`). There is no cancel/abandon edge
  (spec 03 §"no cancel/delete", superseded design **D10** at `03-board-mechanics.md:386`
  only relaxed enough to *archive non-active* tickets).

So a duplicate that reached `working` and then bounced to `blocked` is trapped:
`delete_ticket` refuses it, and the only escape — `AcceptToDone` — is semantically
wrong (it fabricates a completion and, under the merge gate, wants a real landed
commit, 06 §7). Today the user's only recourse is developer intervention.

**Objective:** let the user delete a **blocked** ticket directly from the
ticket-detail sheet — one tap that clears the ticket *and* the worker it holds —
starting with the duplicate-stuck-in-development case, with the affordance built
to extend to other states later.

## What already exists (the pipeline to extend)

Deleting a ticket from the client is already a fully wired, four-hop path. It is
gated to shaping proposals at **every** hop; this design widens the gate to
**blocked** and repairs the one hop (the board) that mechanically cannot yet
service an active ticket.

| Hop | Location | Does today | Gate today |
| --- | --- | --- | --- |
| 1. Button | `TicketDetail.tsx:280` (`data-role="detail-delete"`) | Destructive ghost pill in the bottom-left lead cluster; fires `onDelete(ticket.id)` | `canDelete = isShaping && onDelete` (`:165`), cluster gate `showVoice \|\| canDelete` (`:273`) |
| 2. Client wiring | `PrimaryScreen.tsx:55` `onDelete` | Optimistic `deleteProposal(id)` hide, then fire-and-forget `deleteTicket(id)` | Passed only as the proposal Delete callback |
| 3. Transport → API | `transport.ts:739` `deleteTicket` → `POST /api/tickets/{id}/delete` → `routes.go:763 handleDeleteProposal` | Looks up title, posts a synthesized NL message to the brain (D5 — client never mutates the board) | Message names it "the proposal %q" |
| 4a. Brain tool | `brain/tools.go:298` `delete_ticket` → `doDeleteTicket:534` → `board.ArchiveTicket` | Archives the ticket | Description: "Only backlog or done tickets can be deleted; resolve an in-progress ticket first" (`:301`); prompt `prompt.go:99` |
| 4b. Board | `board/service.go:460` `ArchiveTicket` | Stamps `ArchivedAt`, emits `board.updated` + `feed.updated` | **Refuses active** (`:462`); emits **no** `agent.release` |

Hop 4b is the true blocker. Everything above it is a matter of relaxing a
conditional and adjusting copy; 4b needs a real mechanical addition — release the
worker on archive — and one invariant reconciliation.

## Design decisions

### D1 — Reuse the existing Delete affordance; widen its state gate. Do not add a new button.

The button the brief asks for already exists, in the exact place the brief
specifies (bottom-left `ticket-detail-lead-actions`, `TicketDetail.tsx:274`), with
the exact styling the brief asks for (destructive ghost pill, `--danger` text /
`--danger-soft` border, trash icon, fills solid `--danger` on hover on the primary
surface — `TicketDetail.css` `detail-delete` rules). Adding a second button would
fork the vocabulary. The change is to make the **one** Delete affordance appear in
more states.

Replace the two shaping-only literals with a single predicate so adding a state
later is a one-line edit (extensibility, per the brief):

```ts
// today (TicketDetail.tsx:147, :165, :273)
const isShaping = ticket.state === 'shaping';
const canDelete = isShaping && onDelete !== undefined;
// … cluster: {(showVoice || canDelete) && …}

// proposed
const DELETABLE_STATES = new Set<Ticket['state']>(['shaping', 'blocked']);
const canDelete = DELETABLE_STATES.has(ticket.state) && onDelete !== undefined;
```

The lead-cluster gate becomes `showVoice || canDelete` unchanged — but now renders
on a blocked sheet (where `showVoice` is false and `canDelete` is true), pinning
Delete to the bottom-left while the blocked state's trailing primary action
(**Talk to unblock**, `TicketDetail.tsx:326`) stays right, exactly as Accept does
on a proposal. The `margin-right:auto` on the lead cluster already produces this
split-row layout with no CSS change.

### D2 — The board releases the worker on archiving a blocked ticket (the core fix).

Model it on `AcceptToDone`, which is the *only* existing active→non-active exit and
already does exactly the teardown we need (`board/service.go:289–303`): clear the
worker binding, and emit **`agent.release`** (recycle/tear down the freed worker,
05 §4) + **`pull.evaluate`** (let a waiting ready ticket claim the freed capacity)
in the same outbox transaction. Deleting a blocked ticket is *AcceptToDone's
release mechanics, minus the completion* (no `DoneCommit`, no `feed.completion`
card), *plus the archive tombstone*.

Relax `ArchiveTicket`'s refusal so **blocked** is permitted (working stays refused,
see D3); when the archived ticket was blocked, null its `WorkerID` and emit
`agent.release` + `pull.evaluate` alongside the existing `board.updated` +
`feed.updated`:

```go
func archive(t *Ticket) …:
    if t.State == StateWorking {                 // v1: still refuse working (D3)
        return &ErrInvalidTransition{From: t.State, Attempted: "ArchiveTicket"}
    }
    now := ...
    t.ArchivedAt = &now
    if t.State == StateBlocked {                  // release-on-archive
        worker := *t.WorkerID
        t.WorkerID = nil                          // free the capacity slot
        // keep t.State = blocked and t.BlockedReason as historical truth (see below)
        emit(agent.release{worker}); emit(pull.evaluate)
    }
    emit(board.updated); emit(feed.updated{Verb: archived})
```

**The invariant reconciliation (the subtle part).** `archived_at` is a tombstone
*orthogonal to `State`* — existing archive leaves `State` untouched (an archived
done ticket stays `done`, filtered from reads by `archived_at IS NULL`). So a
deleted blocked ticket keeps `State = blocked`. But invariant **I3**
(`(state IN ('working','blocked')) = (worker_id IS NOT NULL)`,
`0001_board.sql:21`) then forbids nulling its `worker_id`. We must free that slot —
leaving `worker_id` set would keep the worker derived-busy forever (capacity leak).

Resolve it by scoping I3 to the **live board** — an archived row is off the board
and off the pull, so the live-board worker invariant should not bind it:

```sql
-- migration: I3 binds only live rows
ALTER TABLE tickets DROP CONSTRAINT <i3>;
ALTER TABLE tickets ADD CONSTRAINT <i3>
  CHECK ((archived_at IS NOT NULL)
         OR ((state IN ('working','blocked')) = (worker_id IS NOT NULL)));
```

This keeps archive semantics uniform — *stamp the tombstone, release the worker,
never rewrite `State`* — and preserves the historical truth that the ticket was
blocked (and why: `blocked_reason` is kept, so **I4** is untouched). The
`one_active_ticket_per_worker` index (**I2**) needs no change: with `worker_id`
now NULL the row indexes a distinct NULL key and references no worker, so capacity
is correctly derived free.

**Alternative considered — transition `State` to a non-active value on archive
(Option A).** Avoids the CHECK migration, but the board has no "cancelled" state,
so it would fabricate a `ready`/`shaping`/`done` residual that misreads in history
and makes blocked-archive behave unlike every other archive (which preserves
`State`). Rejected: inventing a state to satisfy a constraint is worse than
scoping the constraint to what it actually describes.

### D3 — v1 scope: blocked only. Working and done deferred.

- **blocked** — the driving case. The agent is stalled by definition (waiting on a
  human), so releasing it discards nothing in flight. **In scope.**
- **working** — a live agent is mid-turn; deleting it means killing in-flight work,
  a louder action wanting discard-in-flight semantics and a stronger confirm. The
  board keeps refusing it and the button stays hidden (`DELETABLE_STATES` omits it).
  **Deferred.**
- **shaping** — already shipped; unchanged.
- **ready** — already board-archivable (non-active, no worker). Extending the
  button here later is **frontend-only** (add to `DELETABLE_STATES`); no backend
  change. Left out of v1 to keep the change tight, but it's the cheapest next step.
- **done** — board-archivable, but "delete a finished ticket" is a distinct
  "remove from history" intent, not this feature. **Deferred.**

### D4 — Confirmation is state-dependent: confirm on blocked, keep shaping frictionless.

Deleting a shaping proposal is cheap and re-proposable, so it stays a
fire-and-forget optimistic hide (no confirm) — unchanged. Deleting a **blocked**
ticket is materially more consequential: it tears down a live worker/sandbox and
discards partial development work, and there is **no un-archive affordance** in the
product, so from the user's seat it is irreversible. Gate the active-state delete
behind a lightweight confirm, using the app's established pattern —
`window.confirm` (as in `ResetSessionButton.tsx:17` and the "Clear all
notifications?" prompt at `PrimaryScreenView.tsx:290`) — with copy that names the
consequence:

> **Delete this blocked ticket?** Its in-progress work will be discarded and can't
> be recovered here.

Only on confirm does the client fire the delete. This keeps the safety in the
*interaction*, not in ever-louder chrome.

### D5 — No extra "danger" styling beyond what the button already carries.

The button already reads unmistakably destructive (red text, red-soft border,
trash icon, solid-red hover fill via the `--danger`/`--danger-soft` tokens). It
sits in the subordinate left lead cluster, below the state's right-aligned primary
action — the correct rank for a secondary destructive action. Making it *louder*
than Accept/Talk would misrank it. The confirm dialog (D4), not heavier styling, is
the guardrail. So: reuse the existing `detail-delete` treatment verbatim in the
blocked state — no new CSS.

### D6 — Keep the delete brain-mediated (D5 architecture); update the tool's contract.

The client never mutates the board directly (D5); it posts an intent and the brain
decides. Blocked delete rides the same `POST /api/tickets/{id}/delete` → synthesized
message → `delete_ticket` path. Two contract edits let the brain service a blocked
ticket:

- **API copy** (`routes.go:763`): generalize `handleDeleteProposal`'s message from
  "the proposal %q" to name the ticket by state ("the blocked ticket %q" / "the
  ticket %q"), and rename it `handleDeleteTicket`. The synthesized instruction
  already says "Delete that ticket now; do not ask for confirmation" — the user's
  confirm happened client-side (D4).
- **Tool + prompt** (`tools.go:299–301`, `prompt.go:99`): replace "Only backlog or
  done tickets can be deleted; resolve an in-progress ticket first" with language
  that permits a blocked ticket — e.g. "Backlog, blocked, or done tickets can be
  deleted; deleting a blocked ticket releases its worker. A *working* ticket must
  be resolved first." The board remains the enforcement point (it still refuses
  working), so this is guidance, not the guard.

## UI / UX

A blocked ticket's detail sheet gains the Delete button in the bottom-left,
mirroring the proposal footer's split-row layout:

```
┌──────────────────────────────────────────────┐
│  Add dark-mode toggle          ● Blocked    × │  ← header + lifecycle badge
│                                                │
│  Duplicate of #241 — already in progress       │  ← blocked_reason (detail-blocked-reason)
│                                                │
│  <ticket body / markdown>                      │
│                                                │
│  🗑 Delete            👉 Poke   🎙 Talk to unblock │  ← footer: Delete pinned left,
└──────────────────────────────────────────────┘        Poke + Talk trailing right
```

- **Layout.** Delete joins the `ticket-detail-lead-actions` cluster (bottom-left,
  `margin-right:auto`), which today only renders on a proposal. On blocked it now
  renders holding just Delete (no mic — voice is shaping-only), and the state's
  existing trailing actions (Poke `:309`, Talk `:326`) stay right. No new layout
  primitive; the split row is the same one Accept/Delete already produce on a
  proposal.
- **Tap flow.** Tap Delete → `window.confirm` (D4) → on confirm the client
  optimistically removes the ticket's card from view and posts the delete intent;
  the brain archives it, the board releases the worker, and the next board snapshot
  makes the removal authoritative. On cancel, nothing happens.
- **Optimistic removal.** `PrimaryScreen.tsx:59` uses `deleteProposal(id)`, which
  hides a *proposal* card — proposal-specific. A blocked ticket surfaces as a
  **blocker card** (08 §"Blocker") and a board tile, so the blocked branch needs
  the corresponding optimistic hide (or simply lean on the imminent snapshot/
  `feed.updated{archived}` to drop the card, accepting a brief flicker). Recommend a
  small feed-store helper symmetric with `deleteProposal` that retracts the blocker
  card, so the tap feels instant.
- **Feedback.** `ArchiveTicket` already emits `feed.updated{Verb: archived}`, which
  retracts the blocker card. Optionally add an `activity.toast` ("Deleted") for a
  confirming pulse, matching the finished-toast pattern; not required for v1.

### Answers to the review questions

- **Destructive immediate, or confirm?** Confirm — but only for the *blocked*
  (active) case (D4). Shaping stays immediate. The rule is "confirm scales with
  consequence," not a blanket dialog.
- **Visually distinct / red styling?** It already is, and that's sufficient (D5).
  The button is a red destructive pill sitting subordinate to the primary action;
  the confirm dialog carries the "are you sure," so no louder treatment is added.
- **Other states in this initial design?** Only **blocked** ships (D3). `ready` is a
  frontend-only follow-on (already board-deletable); `working` and `done` are
  deferred as distinct, louder intents. The `DELETABLE_STATES` set (D1) is the seam
  that makes each later addition a one-line change.

## Full-stack change map

| Hop | File | Change |
| --- | --- | --- |
| Button gate | `frontend/TicketDetail.tsx:165,273` | `isShaping` → `DELETABLE_STATES.has(state)`; cluster renders on blocked |
| Client wiring | `frontend/PrimaryScreen.tsx:55` | Wire `onDelete` for blocked: `window.confirm` gate + blocker-card optimistic hide |
| Feed store | `frontend/stores/feed-store.tsx` | Optional helper to retract a blocker card (symmetric with `deleteProposal`) |
| API | `backend/internal/api/routes.go:763` | Rename → `handleDeleteTicket`; state-aware message copy |
| Brain | `backend/internal/brain/tools.go:299`, `prompt.go:99` | Tool/prompt permit deleting a blocked ticket |
| Board | `backend/internal/board/service.go:460` | Permit blocked; null `WorkerID`; emit `agent.release` + `pull.evaluate` |
| Migration | `backend/internal/board/postgres/migrations/000N_*.sql` | Scope invariant **I3** to live rows (`archived_at IS NOT NULL OR …`) |

No wire-schema change: the delete intent is an existing endpoint returning the
existing `MessagePostResponse`, and no new field crosses the client↔server
contract. No new outbox topic: `agent.release` and `pull.evaluate` already exist
(`board/outbox.go`), so the outbox CHECK is untouched.

## Rationale summary

| Decision | Choice | Why |
| --- | --- | --- |
| Affordance | Widen the existing Delete button's state gate | The button, its place, and its styling already exist; a second button forks the vocabulary |
| Board fix | Release-on-archive for blocked, modeled on `AcceptToDone` | Reuses the only existing active→non-active teardown; adds no new topic |
| Invariant | Scope I3 to live rows, keep `State`/`blocked_reason` | Archive stays "tombstone + release," preserves history, no fabricated state |
| Scope | Blocked only in v1 | The driving case; releases nothing in flight. Working/done are louder, distinct intents |
| Confirm | State-dependent — confirm on blocked, none on shaping | Confirm scales with consequence; deleting worker-bound work is irreversible here |
| Styling | Reuse the destructive pill as-is | Already reads destructive and correctly subordinate; the confirm is the guardrail |
| Routing | Keep brain-mediated (D5) | Client posts intent, brain decides; consistent with the shaping delete already shipped |
| Extensibility | `DELETABLE_STATES` predicate | Each later state (ready next) is a one-line change |

## Out of scope / deferred

- **Deleting a *working* ticket** — needs discard-in-flight semantics (kill a
  mid-turn agent) and a stronger confirm. Board keeps refusing it; button stays
  hidden.
- **Deleting a *done* ticket from the sheet** — a "remove from history" intent, not
  this feature; the board already permits the archive, only the button is withheld.
- **Un-archive / restore** — no product affordance to recover an archived ticket.
  The confirm copy is honest about that; a restore flow is a separate feature.
- **First-class duplicate detection** — "duplicate" remains the brain's judgment and
  the user's call at tap time; no dedup type or auto-merge is introduced.
- **`ready`-state Delete button** — mechanically free (already board-archivable),
  but held for a follow-on to keep this change scoped to the blocked case.

## Testing (three-level gate — see end-to-end-development)

- **Unit (board).** `ArchiveTicket` from `blocked` nulls `WorkerID`, keeps `State`
  and `blocked_reason`, stamps `ArchivedAt`, and emits `agent.release{worker}` +
  `pull.evaluate` + `feed.updated{archived}`; from `working` still returns
  `ErrInvalidTransition`; from shaping/ready/done unchanged. Flip
  `service_archive_test.go:96 TestArchiveTicket_ActiveIsRefused` to assert blocked
  now succeeds (working still refused). Add a migration test that a blocked+archived
  row with NULL `worker_id` satisfies the relaxed I3.
- **Unit (brain/api).** `delete_ticket` on a blocked ticket reaches
  `ArchiveTicket`; `handleDeleteTicket` synthesizes a state-aware message and
  returns 202.
- **Component (frontend).** A blocked `TicketDetail` renders `detail-delete` in the
  lead cluster with Talk trailing; a working sheet renders no Delete; tapping Delete
  fires `window.confirm` and only on confirm calls `onDelete`.
- **End-to-end.** A ticket driven to blocked, deleted from the sheet, disappears
  from the board and its blocker card retracts; its worker is released and a waiting
  ready ticket pulls into the freed slot.
