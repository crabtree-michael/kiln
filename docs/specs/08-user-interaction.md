# Kiln — User-Interaction Model (v1)

**Date:** 2026-07-04
**Status:** Accepted

**Scope:** v1, single project, single user
**Relationship to** `01`**–**`07`**,** `09`**:** Defines the **primary screen** — the UX the
`Kiln Voice Screen` design settled: a backlog of orchestrator notifications the user
reads, and a voice channel they talk through (`09`). The `07` text client is demoted to
a **debug view** and survives unchanged (§6). Builds on `04`'s transport (SSE + POST),
amends `06`'s tool set (§5, §7), and gives `10` the notification content it will later
push. The board visualization stops being the product surface; the *feed* is.

## 1. Purpose & scope

This document decides:

- The **screen structure**: header, backlog, activity row, voice dock (§2).
- The **feed model**: what a backlog item is, where each kind lives, and its lifecycle
— the hybrid of derived cards and brain-authored notifications (§3).
- The **activity layer**: the thinking indicator, action toasts, and the reply pill
(§4).
- The **acceptance gate**: ticket proposals in the backlog and what accepting does
(§5).
- **Client structure** and the fate of the `07` client (§6).
- **Backend touchpoints** and `06` amendments (§7); **testing** (§8).

Out of scope: the mic, transcript, and utterance commit (`09`); push transport and
tap-to-open deep links (`10`); auth (`02` §12).

## 2. The screen

Mobile-first, one screen, four stacked regions (per the design's 4a–4d / 5a / 6a–6b
states):

- **Header** — the Kiln mark and a one-line status summary derived from the feed:
`1 blocker · 4 updates` when something needs the user, `5 streams · nothing needs you` when not, `all clear` when the feed is empty. (A *stream* is the design's word
for a ticket's thread of work; the label on a card is its ticket title.)
- **Backlog** — one scrolling list, strictly ordered: **unresolved blockers** pinned on
top (ember dot, `Blocker` tag, question in full), then **pending proposals** (§5),
then the **"While you were away"** divider, then unseen **updates** newest-first
(ticket label + relative age). Update cards may embed an image preview (4c). Empty
feed renders the 4d "All clear" state with a streams status line
(`3 building · 2 idle · last word 6m ago`).
- **Activity row** — between backlog and dock; hidden when idle (§4).
- **Dock** — the live transcript and mic (`09` §3–§4).



## 3. The feed

**Hybrid sourcing** (§9, D1): cards that correspond to board facts are **derived** from
board state so they cannot go stale; cards that are pure communication are
**brain-authored rows**. The client renders one list and never knows the difference.


| Card                 | Source                                                                             | Appears when            | Leaves when                                                            |
| -------------------- | ---------------------------------------------------------------------------------- | ----------------------- | ---------------------------------------------------------------------- |
| **Blocker**          | Derived: ticket in the Blocked zone (`03`) — its `blocked_reason`, blocked-at time | Ticket enters Blocked   | Ticket leaves Blocked (brain resumes/accepts) — *never* by being seen  |
| **Proposal**         | Derived: Shaping ticket with `approval_requested` set (§5)                         | Brain requests approval | Ticket marked Ready (tap or voice accept), or brain withdraws/reshapes |
| **Update / preview** | New `notifications` table, brain-authored (§7)                                     | Brain posts it          | Seen by the user, or retracted by the brain                            |


**Lifecycle: an inbox that drains** (user decision — §9, D2). The brain curates the
feed — it posts updates, retracts ones that stopped mattering, and clears blockers by
actually unblocking work. Updates additionally clear themselves: **seen means gone**.

- **Seen semantics.** When update cards render on a foregrounded, visible screen, the
client acks `POST /api/feed/seen {last_notification_id}`; the server stamps
`seen_at` on every unseen notification with `id ≤ last`. Seen updates drop out of
subsequent feed snapshots. Blockers and proposals ignore seen entirely — they persist
until resolved.
- **Session hold.** The client keeps already-acked updates on screen for the rest of
the session (they fall below "While you were away" with their ages); a fresh open
shows only blockers, proposals, and whatever arrived unseen since. This is what makes
the divider mean *while you were away*.
- **Live arrivals** during a session render at the top of the updates section aged
`now`, and are acked like any rendered update.

**Transport.** Same pattern as the board (`04` D7): absolute snapshots, no deltas. A
new SSE event `feed` carries the full visible feed (derived cards assembled from
board state + unseen notifications); `GET /api/feed` serves the same snapshot for
initial render. Snapshots are emitted via a new outbox topic `feed.updated`,
appended transactionally by whatever changed the feed: board operations that touch
Blocked/`approval_requested` state append it exactly as they append `board.updated`
(`03` §7), and notification posts/retracts/acks append it likewise. Reconnect = the
first `feed` event is the resync.

## 4. The activity layer

Everything in the activity row is **ephemeral — SSE only, never stored** (§9, D3). A
disconnected client misses toasts and loses nothing durable: facts worth keeping are in
the feed or on the board.

A new SSE event `activity` with three payload kinds:

- `thinking` `{on: true|false}` — emitted by the runtime's event worker when a
brain pass starts and ends (any event type). Renders the 6a spinner:
"Kiln is thinking…".
- `toast` `{verb, ticket_title}` — one per side-effect board transition the brain
causes: dispatched (`Started <title>`), new turn sent (`Nudged <title>`), accepted
(`Finished <title>`), marked ready (`Queued <title>`). Emitted mechanically: board
operations append an `activity.toast` outbox row alongside their `board.updated`;
the executor is an SSE broadcast. Renders as the 6b pill, auto-dismissing (~4 s).
- `say` — not a new event: the brain's existing `say` SSE reply (`07` §4) renders
in the **same pill**, but *persistent* — it stays until the user's next utterance
commits or they dismiss it. The pill is Kiln's half of a live exchange (user decision
— §9, D4); the transcript history behind it lives only in the debug view. `say` also
remains the brain's answer channel in conversation memory (`06` §3) — unchanged.

Pill contention resolves simply: a `say` reply replaces any toast on screen; toasts
queue behind an active `say` and drain when it dismisses; `thinking` renders only when
the pill is empty.

## 5. The acceptance gate (Shaping, realized)

Shaping (`01` §5) gets its interaction surface. The gate is **at the brain's
discretion** (user decision — §9, D5): the brain is prompted to seek human approval
when a ticket embeds complex technical decisions, and to proceed on its own for
routine work.

- **New board fact:** a Shaping ticket can have `approval_requested` set. A new
brain tool `request_approval(ticket)` sets it; the ticket surfaces as a proposal
card (title + shaped summary + Accept affordance). The brain's `mark_ready` tool
remains for the no-gate path (`06` §4).
- **Tap Accept** → `POST /api/tickets/{id}/accept` → **mechanical** `MarkReady` with a
strict precondition (Shaping + `approval_requested`), emitting `pull.evaluate`,
`board.updated`, and `feed.updated` like any Ready transition (`03` §7). No LLM pass:
acceptance is the human's own decision, already fully shaped — waking the brain to
relay it would add latency and nondeterminism for nothing (§9, D6). This is a
deliberate, narrow exception to `07` D5's "all mutation flows through the brain":
one idempotent, precondition-guarded transition.
- **Voice accept** ("yes, run it") flows through the brain as ordinary conversation →
`mark_ready`. Both paths converge on the same board operation.
- **Decline or amend** has no button: the user says so, and the brain reshapes or
drops the ticket. Talking is the interface for everything non-mechanical.



## 6. Client structure

- **Routes:** `/` is the primary screen; `/debug` is the `07` client, whole and
unchanged — board columns, chat panel, text box. It stays the development window into
raw state (and the text path for exercising the message seam without a mic).
- **New stores** beside `07` §5's two: a **feed store** (latest `feed` snapshot +
§3's session-held seen updates) and an **activity store** (thinking flag, pill
content + queue). Same rules as ever: snapshots replace wholesale; stores are
context + reducer, no state library (`07` D4).
- **Transport:** the existing thin module gains the `feed`/`activity` SSE events and
the `/api/feed`, `/api/feed/seen`, `/api/tickets/{id}/accept` calls — generated
types from `/schema`, as always (`02` §3).
- The dock region embeds `09`'s voice module.



## 7. Backend touchpoints

- `notifications` **table** (runtime module, beside `messages` — `07` §3): `id bigserial`, `kind` (`update`/`preview`, CHECK), `ticket_id` nullable, `body text`,
`image_url` nullable, `created_at`, `seen_at` nullable, `retracted_at` nullable.
Append + stamp only; no edits.
- **Feed assembly** (runtime service): one query joining derived cards (board port:
Blocked tickets, approval-requested Shaping tickets) with unseen, unretracted
notifications → the `FeedSnapshot` wire shape. Used by both `GET /api/feed` and the
`feed.updated` executor.
- **Outbox topics:** add `feed.updated` and `activity.toast` to the CHECK (`04` §2);
executors are SSE broadcasts on the hub. Board ops that change Blocked or
`approval_requested` state append `feed.updated` transactionally (`03` §7);
`activity.toast` rides the same appends for the §4 verbs. The notification store
appends `feed.updated` in its own write transactions too — amending `04` §2's
"written by the board" column: the outbox gains the runtime's notification store as
a second writer, under the same transactional-emission rule.
- **Brain tools** (`06` §4 amendments): add `request_approval(ticket)`,
`post_update(body, ticket?, image_url?)`, `retract_update(notification_id)`; prompt
guidance for the §5 discretion rule and for posting updates worth a glance, not a
play-by-play. `say` is unchanged.
- **api module:** routes `GET /api/feed`, `POST /api/feed/seen`,
`POST /api/tickets/{id}/accept`; `feed`/`activity` fan-out on the hub. All shapes in
`/schema`.
- `10` **inherits:** `notify.send` stays log-only for now. When push lands, blockers
and proposals (derived) plus notifications (authored) are exactly the content push
delivers; tapping a push opens `/` to an already-correct feed — no new state.



## 8. Testing

- **Unit (backend):** feed assembly — derivation rules, seen filtering, ordering
(blockers → proposals → unseen updates newest-first); seen high-water stamping;
accept route preconditions (rejects non-Shaping, rejects without
`approval_requested`, idempotent on replay); toast emission per transition verb.
- **Unit (frontend):** feed store (snapshot replace + session hold across acks),
activity store (pill contention: say replaces toast, toasts queue, thinking only
when empty; auto-dismiss timing), seen-ack firing only when visible.
- **Image snapshots (**`02` **§4a):** backlog with blocker on top (4a), updates-only (4b),
embedded preview (4c), all-clear (4d); pill in thinking (6a), toast (6b), and
`say`-reply states; a proposal card with Accept.
- **E2E (**`02` **§14):** the full loop through the primary screen against the mock
provider — message in (text seam) → proposal card appears → tap Accept → pulled,
`Started` toast → mock turn completes → blocker card pinned → answer → blocker
clears, update posts → seen → drains. The `07` e2e keeps covering the debug view.



## 9. Decision log


| #   | Decision                                                                                                               | Alternatives considered                                                                                   | Rationale                                                                                                                                                                                                                                                             |
| --- | ---------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| D1  | Hybrid feed: blockers/proposals derived from board state; updates in a brain-authored `notifications` table.           | One brain-managed `feed_items` table for everything; fully derived projection with no new state.          | Derived cards cannot drift from ticket truth and the brain already "removes" them by doing its job; authored updates need storage and retraction anyway. One table for all would let blocker cards go stale; fully derived would make updates read like chat history. |
| D2  | Inbox that drains: seen-means-gone for updates; blockers/proposals persist until resolved; brain curates and retracts. | Append-only feed with a last-seen divider; bounded recency window.                                        | User decision. The screen should tend toward "All clear" — the feed is a to-attend list, not a log. History belongs to the debug view and the transcript.                                                                                                             |
| D3  | Activity layer is ephemeral SSE, never stored.                                                                         | Persist toasts as feed rows; derive thinking from queue-table polling.                                    | A toast repeats what the board/feed already record durably; storing it would double-write one fact. Missing a toast while disconnected loses nothing.                                                                                                                 |
| D4  | `say` replies render in the same pill as toasts, persistent until the next utterance or dismissal.                     | A chat strip on the primary screen; replies as feed items.                                                | User decision. The pill is the live-exchange surface; the primary screen deliberately has no chat history (that is `/debug`). Feed items are for things that must survive being away.                                                                                 |
| D5  | Approval gate at the brain's discretion; prompted to gate complex technical decisions.                                 | Mandatory approval for every ticket; no gate (status quo).                                                | User decision — this is what Shaping means. Mandatory gating would tax routine work; no gate wastes the backlog's decision surface.                                                                                                                                   |
| D6  | Tap-accept is a mechanical `MarkReady` via `POST /api/tickets/{id}/accept`, a narrow exception to `07` D5.             | Route acceptance through the brain as a `human.message`; a structured `human.ticket_accepted` event type. | The decision is already made and the transition deterministic; an LLM pass adds seconds and nondeterminism to a button. Strict preconditions keep it as safe as the board's other ops. Voice acceptance still goes through the brain; both converge on `MarkReady`.   |
| D7  | Spec numbering: this doc is `08`, voice is `09`.                                                                       | Voice as `08` (user listed it first).                                                                     | Every existing cross-reference (`04` §6–§7, `06`, `07`) already names `09` as the voice spec and `10` as push; keeping them true is worth more than list order.                                                                                                       |


**Open questions (owned elsewhere or later):** push payload mapping and deep links
(`10`); where preview images come from — agent artifacts need a storage/URL story
(`05`-adjacent, future); notification retention/pruning (`02` §15, with the
transcript); whether the header's stream-status line needs worker liveness beyond board
state (`05` §4's reconciler may already suffice); multi-blocker ordering beyond
blocked-at (revisit if real usage stacks blockers).