# Kiln — v1 Text Client (v1)

**Date:** 2026-07-03
**Status:** Proposed
**Scope:** v1, single project, single user
**Relationship to `01`–`06`:** Reshapes `02` §11 (web client) for a deliberate v1 descope:
**voice (`02` §9) and notifications (`02` §10) are deferred** while the backend is built
and needs something real to test against. The client is a board visualization plus a text
conversation with the brain. The descope is a thin wrapper removed, not a fork: voice was
always STT → brain → TTS (`01` §3), so text talks to the same seams voice will
(§10, D1). Amends `04` §7's names accordingly (§4; §10, A1).

## 1. Purpose & scope

This document decides:

- What v1 ships without voice/notifications, and what that defers (§2).
- The **transcript**: persisted server-side, and where it lives (§3).
- The **client contract**: endpoints, events, and the `04` amendments (§4).
- The **frontend stack** and internal layering (§5).
- Backend touchpoints of the descope (§6).
- UI structure, reconnection, and error surfaces (§7–§8).
- Testing, including the `02` §4a image-snapshot targets (§9).

Out of scope: STT/TTS and mic/audio (`09`); push, deep links, and the PWA-vs-wrapped-native
decision — that fork is gated on mobile mic+push constraints, so it waits for `09`/`10`
(§10, D2). Auth stays with `02` §12 (v1 local-only).

## 2. The descope

**In v1:** the full `01` §2 loop, driven by typing instead of speaking — create and shape
tickets by chat, watch the board move live, answer blockers in the chat panel.

**Deferred, and what stands in:**

| Deferred | v1 stand-in |
| --- | --- |
| Voice input (`02` §9) | Text box → `POST /api/message` → the same `human.message` event |
| Kiln's voice replies | `say` SSE events rendered in the chat panel |
| Push when Blocked (`02` §10) | The Blocked zone is the top-most surface on screen (§7); `notify.send` executes log-only (§6) |
| PWA/native packaging | Plain responsive web app |

Nothing in `03`–`06` changes when the deferred pieces land: `09` puts STT in front of
`/api/message` and TTS on top of `say`; `10` gives `notify.send` a real executor.

## 3. The transcript

**Persisted server-side** (user decision — §10, D3), and made load-bearing rather than a
UI convenience: the transcript is the **conversation memory the brain reads** (last 20
messages per pass — `06` §3), which is what makes multi-turn shaping coherent and closes
`03`'s open question about where the shaping conversation lives.

- One table, **`messages`**, owned by the **runtime module** (it already owns the
  client-facing conversation surfaces — `04` §7): `id bigserial`, `role` (`user`/`kiln`,
  CHECK), `text`, `created_at`. Append-only; no edits, no deletes in v1.
- **User side**: `POST /api/message` appends the `user` row and enqueues the
  `human.message` event *in one transaction* — the transcript and the event queue cannot
  disagree.
- **Kiln side**: the brain's `say` tool calls the runtime's **Say port**: append the
  `kiln` row, then push a `say` SSE event to connected clients. Append-then-push; a crash
  between them costs a live push, not history — the client reconciles on next fetch.
- **Brain side**: a **ConversationReader port** (`Recent(n)`) serves `06` §3's context
  block.
- Unbounded growth is accepted for v1 (single user; text). Retention/pruning is a `02`
  §15 concern.

## 4. Client contract — amendments to `04` §7 (A1)

Voice-named seams become text-neutral; `09` later feeds the same seams. Renames touch
`04` §7's table, the runtime scaffold's event constant + events-table CHECK, and the api
route stubs:

| Was (`04`) | Now | Contract |
| --- | --- | --- |
| event `human.voice_input` | **`human.message`** | Payload `{text}`; enqueued by `POST /api/message`. |
| `POST /api/voice` | **`POST /api/message`** | Body `{text}` (non-empty, ≤ 4k chars) → transactional append + enqueue (§3) → `202 {event_id, message_id}`. |
| SSE `speak` | **`say`** | Payload `{message_id, text, at}` — one event per brain `say`. |
| — | **`GET /api/messages?limit=50`** | Most-recent `limit` transcript rows, oldest-first. New endpoint for initial render and post-reconnect reconciliation. |

Unchanged from `04`: `GET /api/stream` (now carrying `board` + `say` events), `GET
/api/board`, absolute snapshots, reconnect = fresh snapshot. All shapes live in `/schema`
(`02` §3) — this spec adds `Message`, the `say` event, and the `/api/message(s)` pair to
the schema.

## 5. Frontend stack & layering

Filling `02` §11's "framework and build" within the `02` §3–§4 guardrail philosophy
(machine-checkable, minimal moving parts):

- **Vite + React + TypeScript strict**, with the escape-hatch bans from `02` §4b
  (`any`, `as`, `@ts-ignore`, non-null `!`) enforced by ESLint; Prettier for format.
- **Types generated from `/schema`** via `openapi-typescript` (the Go side already
  generates `internal/wire` from the same schema) — the client never hand-writes a wire
  type.
- **No state library, no component library, no CSS framework** (§10, D4): state is two
  React contexts —
  - **board store**: holds the latest `Snapshot`; every `board` SSE event replaces it
    wholesale (`04` D7 — no merging, no diffing);
  - **chat store**: transcript page from `GET /api/messages` + `say` events appended +
    the user's own sends appended optimistically (reconciled by `message_id`).
- **Transport layer**: one thin module wrapping `EventSource` (`/api/stream`) and `fetch`
  (generated types), the only code that knows URLs. `EventSource` auto-reconnect is the
  reconnection strategy; on every stream (re)open the client refetches `/api/messages`
  once to fill any gap (board needs nothing — the first `board` event is the resync).
- Layout per `02` §11: transport → stores → presentational components; mic/audio and push
  land later as sibling modules without touching these.

## 6. Backend touchpoints

Beyond the §4 renames, the descope means:

- **`notify.send` executor is a structured log line** in v1 (topic, ticket, reason) — the
  outbox contract (`03` §7.1) is unchanged, so `10` swaps the executor, nothing else.
- **Say port + ConversationReader port + `messages` table/migration** land in the runtime
  module (§3); the api module gains the `/api/message` + `/api/messages` routes and the
  `say` fan-out on the hub (both are stubs to rename/extend, not new surfaces).
- The brain's dead-letter `say` (`06` §8) uses the same Say port — no special path.

## 7. UI structure

Mobile-first, one screen, two stacked regions (`01` §4's thin disposable client):

- **The board** (top ~60%): three columns — Backlog (Shaping over Ready), Developing
  (**Blocked stacked above Working**, `01` §5), Done. Ticket cards: title, one-line body
  preview, and on Blocked cards the `blocked_reason` in full — with push deferred, this
  zone *is* the notification surface, so Blocked cards are visually loudest on the page.
  A capacity chip shows `WorkerFree/WorkerTotal`. Done column shows the latest handful.
- **The chat panel** (bottom, expandable): transcript + text input. Sending is
  optimistic; a failed `POST` marks the message with a retry affordance. `say` replies
  append as Kiln messages.
- **Ready in pull order** top-to-bottom (`03` §4's `GetBoard` contract) so the user sees
  what gets pulled next.
- No drag-and-drop: the board is a **visualization**; all mutation flows through the
  brain by chat (§10, D5).

## 8. Connection & error surfaces

- **Stream state chip**: connected / reconnecting. `EventSource` retries natively; while
  reconnecting the board dims but stays rendered (stale-but-visible beats blank).
- **On reconnect**: first `board` event replaces state; one `/api/messages` refetch
  reconciles chat (§5).
- **`POST /api/message` failure**: inline on the message bubble; never a modal.
- **Dead-letter visibility**: the brain's system-error `say` (`06` §8) arrives as a
  normal chat message — no separate error channel in the client.

## 9. Testing

- **Unit (frontend)**: stores (snapshot replacement, optimistic reconcile by
  `message_id`, gap refetch on reopen) with a mocked transport; transport module against
  a fake `EventSource`.
- **Image snapshots** (`02` §4a targets): `TicketCard` (each of the five states, blocked
  with long reason), `BoardColumn` (zone stacking), `ChatPanel` (user/kiln/pending/failed
  bubbles), the capacity chip, and the full mobile layout.
- **Backend (runtime/api additions)**: transactional append+enqueue (§3) — crash between
  is impossible by construction, tested against real Postgres; Say port append+push
  ordering; `/api/messages` pagination.
- **E2E** (`02` §14, now concretely): drive the full loop through this client against the
  mock provider — type "build X" → ticket appears in Shaping → shape → Ready → pulled →
  mock turn completes → Blocked appears with reason → answer in chat → resumed → Done.
  This spec is what makes that loop human-visible.

## 10. Decision log

| # | Decision / Amendment | Alternatives considered | Rationale |
| - | -------- | ----------------------- | --------- |
| A1 | Rename `04`'s voice-shaped seams text-neutral: `human.message`, `POST /api/message`, SSE `say`; add `GET /api/messages`. | Keep voice names and send text through them. | User decision. Voice is a wrapper around these seams (`01` §3), so the names should describe the payload; `09` adds STT/TTS without renaming anything back. |
| D1 | Descope voice + notifications, not a parallel "debug UI". | A throwaway admin page beside the real client. | User decision. The text client *is* the product client minus wrappers — everything built here (stores, transport, board rendering, chat) survives `09`–`11` unchanged. |
| D2 | Defer PWA-vs-native packaging to `09`/`10`. | Decide now. | The fork is driven by mobile mic + push constraints — exactly the two deferred surfaces. Deciding without them would be guessing. |
| D3 | Transcript persisted server-side, in the runtime module, and read by the brain. | Ephemeral client-session transcript (server stores nothing). | User decision. Made load-bearing: it doubles as the brain's conversation memory (`06` §3) and closes `03`'s shaping-transcript question — history across refreshes comes free. |
| D4 | No state/component/CSS libraries; two contexts + generated types. | Redux/Zustand; a component kit; Tailwind. | Snapshot-replacement state is too simple to justify a library (`04` D7 did the hard part); every dependency is surface a weak model can misuse (`02` §4). Revisit at `11` if the client grows. |
| D5 | Board is read-only; no drag-and-drop mutations. | Drag cards between columns as a second mutation path. | `01` §3: position changes have mechanical meaning and flow through orchestrator decisions; a second, brain-bypassing path would fork authority. The chat is the input. |

**Open questions (owned elsewhere or later):** transcript retention/pruning (`02` §15);
desktop layout beyond responsive stretching (`11`); whether Done column paginates or
archives (`11`); auth on the endpoints (`02` §12).
