# Spec 08 — Implementation Contract (shared across all agents)

This is the **integration contract** every implementation agent builds to, and that the
three E2E tests assert against. It resolves spec 08 into exact routes, SSE events, port
signatures, topic names, DB shapes, dev seams, and frontend selector conventions.

**Project decision overriding the spec:** tap-Accept **routes through the brain** (not the
mechanical MarkReady of 08 D6). `POST /api/tickets/{id}/accept` appends a user transcript
row + enqueues a `human.message` event (exactly like `POST /api/message`), and the brain
marks the ticket Ready via `mark_ready`. No dedicated `AcceptProposal` board op.

Wire types are **already generated and committed** from `schema/openapi.yaml`
(`make schema`, PATH must include `$(go env GOPATH)/bin` for `oapi-codegen`). Go types in
`backend/internal/wire/generated.go`; TS in `frontend/src/schema/generated.ts`. Do not
hand-edit generated files.

---

## A. Wire surface (already in schema — do not change without re-running `make schema`)

Routes:
- `GET  /api/feed` → `wire.FeedSnapshot`
- `POST /api/feed/seen` (body `wire.FeedSeenRequest {last_notification_id}`) → 202
- `POST /api/tickets/{id}/accept` → 202 `wire.MessagePostResponse {event_id, message_id}`
- `GET /api/stream` now also carries `feed` (`wire.FeedSnapshot`) and `activity`
  (`wire.ActivityEvent`) named SSE events.

New/changed schemas: `wire.Ticket` gains `ApprovalRequested bool` (required).
`wire.FeedCard {Kind, Id, Label, Body, TicketId*, NotificationId*, ImageUrl*, CreatedAt}`,
`Kind ∈ {blocker,proposal,update,preview}`. `wire.FeedSummary {BlockerCount, UpdateCount,
StreamCount, Building, Idle, LastWordAt*}`. `wire.FeedSnapshot {Summary, Cards}`.
`wire.ActivityEvent {Kind ∈ {thinking,toast}, On*, Verb* ∈ {started,nudged,finished,queued},
TicketTitle*}`. `wire.FeedSeenRequest {LastNotificationId int64}`.

---

## B. Board module (`backend/internal/board`) — Agent: BOARD

1. **`approval_requested` fact.**
   - Migration `postgres/migrations/0003_approval_requested.sql`: `ALTER TABLE tickets ADD
     COLUMN approval_requested boolean NOT NULL DEFAULT false;` plus a CHECK tying it to
     shaping: `CHECK (NOT approval_requested OR state = 'shaping')`.
   - `entities.go` Ticket: add `ApprovalRequested bool`.
   - `postgres/store.go`: add to `ticketColumns`, `scanTicket`, `InsertTicket` and
     `UpdateTicket` column lists.
2. **`RequestApproval(ctx, TicketID) (Ticket, error)`** — new `Service` method via the
   `mutate` helper. Precondition `state == shaping` (else a precondition error). Sets
   `ApprovalRequested = true`. Appends `Emission{Topic: TopicFeedUpdated}` inside `apply`.
3. **`MarkReady`** — additionally set `ApprovalRequested = false`; append
   `Emission{Topic: TopicFeedUpdated}` and `Emission{Topic: TopicActivityToast,
   Payload: ToastPayload{Verb: "queued", TicketTitle: t.Title}}` inside its apply.
4. **Toast + feed emissions on the §4 verbs** (each inside the relevant op's apply, beside
   existing emissions):
   - dispatch (pull binds a worker, `pullOnce`): `ToastPayload{Verb:"started", TicketTitle}`.
   - resume / new turn (`SendToAgent` when leaving Blocked): `Verb:"nudged"` + `feed.updated`.
   - `AcceptToDone`: `Verb:"finished"` + `feed.updated`.
   - `MarkBlocked`: `feed.updated` (a blocker card appears). (No toast verb; blocker is a feed card.)
5. **Outbox** (`outbox.go`): add `TopicFeedUpdated Topic = "feed.updated"`,
   `TopicActivityToast Topic = "activity.toast"`, and
   `type ToastPayload struct { Verb string \`json:"verb"\`; TicketTitle string \`json:"ticket_title"\` }`.
   Migration `postgres/migrations/0004_outbox_topics.sql` widening the CHECK:
   `ALTER TABLE outbox DROP CONSTRAINT <name>; ALTER TABLE outbox ADD CONSTRAINT <name>
   CHECK (topic IN ('agent.send','agent.release','notify.send','pull.evaluate',
   'board.updated','feed.updated','activity.toast'));` (find the real constraint name in
   `0002_outbox.sql`).
6. **Dev seed extension** (for E2E preconditions): extend the existing dev seed path
   (`TicketSeeder`) so a seeded ticket may be created directly in `blocked` (with a
   `blocked_reason` + a worker bound to satisfy I3/I4) or in `shaping` with
   `approval_requested=true`. Expose a method the api dev route calls, e.g.
   `SeedTicket(ctx, SeedSpec{Title, Body, State, BlockedReason, ApprovalRequested})`.
   Keep the current no-arg-ish behavior working (default state shaping). If binding a
   worker for a blocked seed is impractical, prefer seeding `shaping+approval_requested`
   for the proposal test and use `mark_blocked` via the real flow for the blocker test —
   BOARD agent decides and documents which, and tells INTEGRATION the exact dev method.
7. Update the "exactly N tools" golden expectation only if it lives in board (it lives in
   brain — coordinate: total brain tools becomes **10**).
8. Unit tests: `RequestApproval` precondition + flag + feed.updated; `MarkReady` clears
   flag + emits queued toast + feed.updated; toast emission per verb.

## C. Runtime module (`backend/internal/runtime`) — Agent: RUNTIME

1. **`notifications` table** — migration `postgres/migrations/0003_notifications.sql`
   mirroring `0002_messages.sql`: `id bigserial PK, kind text CHECK (kind IN
   ('update','preview')), ticket_id text NULL, body text NOT NULL, image_url text NULL,
   created_at timestamptz NOT NULL DEFAULT now(), seen_at timestamptz NULL,
   retracted_at timestamptz NULL;` + index `(id DESC)`.
2. **`notifications.go`**: `Notification` domain type; `NotificationStore` port:
   - `PostNotification(ctx, kind, body string, ticketID, imageURL *string) (Notification, error)`
     — INSERT + append `outbox(topic='feed.updated')` in ONE tx (via `inTx`). This makes the
     runtime a **second outbox writer** (08 §7).
   - `RetractNotification(ctx, id int64) error` — `UPDATE ... SET retracted_at=now()` +
     `feed.updated` in one tx.
   - `MarkSeen(ctx, lastID int64) error` — `UPDATE notifications SET seen_at=now() WHERE
     seen_at IS NULL AND id <= $1` + `feed.updated` in one tx.
   - `UnseenNotifications(ctx) ([]Notification, error)` — `seen_at IS NULL AND retracted_at
     IS NULL`, newest-first.
   Implement on `postgres/Store` (it already implements `MessageStore`).
3. **Feed assembly** — `Service.Feed(ctx) (FeedSnapshot, error)` (domain type in runtime).
   Reads board via a new `boardReader BoardReader` port (`GetBoard(ctx) (board.Snapshot,
   error)`; `*board.Service` satisfies it) and `UnseenNotifications`. Build cards in strict
   order: blockers (from `snap.Blocked`, body=blocked_reason, created_at=UpdatedAt as
   blocked-at proxy) → proposals (from `snap.Shaping` where `ApprovalRequested`, body=Body)
   → updates newest-first (from unseen notifications). Compute `FeedSummary`
   (blocker_count, update_count, stream_count = len(working)+len(blocked), building =
   len(working), idle = stream_count - building, last_word_at from latest notification or
   nil). Return a runtime domain `FeedSnapshot`; api maps to `wire.FeedSnapshot`.
4. **`thinking` bracket** — in `handleEvent` (`service.go:199`), `PushActivity(ctx,
   ActivityEvent{Kind:thinking, On:true})` before `brain.HandleEvent`, and `On:false` after
   (deferred). Events-queue only.
5. **Pusher ports** (mirror `SayPusher`): `FeedPusher { PushFeed(ctx, FeedSnapshot) error }`,
   `ActivityPusher { PushActivity(ctx, ActivityEvent) error }`. Hub implements both.
6. **Topic routing** — add consts `topicFeedUpdated="feed.updated"`,
   `topicActivityToast="activity.toast"`; in `handleOutbox` switch:
   - `case topicFeedUpdated:` assemble `s.Feed(ctx)` then `s.feedPusher.PushFeed(ctx, snap)`.
   - `case topicActivityToast:` unmarshal `ToastPayload` from `e.Payload`, then
     `s.activityPusher.PushActivity(ctx, ActivityEvent{Kind:toast, Verb, TicketTitle})`.
   Both self-heal (log-and-drop on error, like board.updated).
7. **Brain-facing notification ops**: expose `PostNotification`/`RetractNotification` on
   `Service` (delegating to the store) so the brain port (below) is satisfied by `*runtime.Service`.
8. **`Service` struct + `NewService`** gain, in this order, appended AFTER the existing
   `sayer SayPusher` param: `notifications NotificationStore`, `boardReader BoardReader`,
   `feedPusher FeedPusher`, `activityPusher ActivityPusher`. (INTEGRATION updates the call.)
9. Unit tests (`postgres/*_integration_test.go` + service test): feed assembly ordering &
   seen filtering; MarkSeen high-water; notification post/retract emits feed.updated.

## D. Brain module (`backend/internal/brain`) — Agent: BRAIN

1. Three tools (total becomes **10**): `request_approval {ticket}`, `post_update {body,
   ticket?, image_url?}`, `retract_update {notification_id}`. For each: `ToolName` const,
   input struct, `ToolDef` in `Tools`, `dispatchOne` case, `do*` handler.
2. Ports (`ports.go`): `BoardAPI` gains `RequestApproval(ctx, board.TicketID) (board.Ticket,
   error)` (satisfied by `*board.Service`). New `NotificationStore` port:
   `PostNotification(ctx, kind, body string, ticketID, imageURL *string) error` and
   `RetractNotification(ctx, id int64) error` (satisfied structurally by `*runtime.Service`).
   `post_update` uses kind `update`, or `preview` when `image_url` is set.
3. Prompt: bump `CurrentPromptVersion` to 2, add `systemPromptV2` + `promptTemplates[2]`.
   New guidance: when to `request_approval` (complex technical decisions) vs `mark_ready`
   (routine); post updates worth a glance not a play-by-play; retract updates that stopped
   mattering.
4. Update the "exactly seven tools" golden (`dispatch_test.go`) to ten, and pin prompt v2.
5. `NewService` gains a `notifications NotificationStore` param (INTEGRATION passes `rtSvc`).

## E. API + composition — Agent: INTEGRATION (owns hub.go, routes.go, wiring.go, adapters.go, main.go)

1. **hub.go**: `eventFeed="feed"`, `eventActivity="activity"`. `PushFeed(ctx,
   runtime.FeedSnapshot) error` → `feedToWire` → `broadcast(sseFrame{event:eventFeed,...})`.
   `PushActivity(ctx, runtime.ActivityEvent) error` → `activityToWire` → broadcast. (Both
   take data; no new hub constructor dep, no feed-reader cycle.)
2. **routes.go**: register `GET /api/feed` (→ `feedToWire(rtSvc.Feed(ctx))`),
   `POST /api/feed/seen` (→ `rtSvc.MarkSeen(last_notification_id)`, 202),
   `POST /api/tickets/{id}/accept` (→ synthesize acceptance text from the ticket title,
   call `rtSvc.PostMessage(ctx, text)`, return 202 `MessagePostResponse`). New ports:
   `FeedReader{Feed(ctx)(runtime.FeedSnapshot,error)}`, `SeenAcker{MarkSeen(ctx,int64)error}`,
   `TicketAccepter` (reuse `MessagePoster` + a board title lookup, or a small port). Add
   mapping fns `feedToWire`, `activityToWire`, and extend `ticketToWire` with
   `approval_requested`.
3. **Dev seam** for E2E: extend the dev-gated route group (behind `KILN_DEV_ENDPOINTS=1`)
   with whatever BOARD's `SeedTicket` exposes — a `POST /api/dev/tickets` that accepts
   optional `state`, `blocked_reason`, `approval_requested`, and a
   `POST /api/dev/notifications {kind, body, ticket_id?, image_url?}` calling
   `rtSvc.PostNotification`. These let the E2E tests deterministically produce a blocker
   card, a proposal card, and an update card without depending on LLM discretion. Not in
   openapi (dev-only, hand-wired).
4. **wiring.go**: update `runtime.NewService(...)` with the 4 new args (notifications =
   `runtimepg.New(db)`, boardReader = `boardSvc`, feedPusher = `hub`, activityPusher =
   `hub`); update `brain.NewService(...)` with `notifications = rtSvc`; wire the new api
   ports (all satisfied by `rtSvc`/`boardSvc`) into `api.NewServer`.
5. Backend `make check` (lint + `go build ./...` + `go test ./...`) must pass.

## F. Frontend (`frontend/src`) — Agents: FE-DATA then FE-VIEW

Router: `pnpm add react-router-dom`. `main.tsx` wraps `<BrowserRouter>` with `<Routes>`:
`/` → `<PrimaryScreen/>`, `/debug` → existing `<App/>` (kept whole, unchanged).

Transport (`transport.ts`): add `onFeed`/`onActivity` (optional) to `StreamHandlers`;
`addEventListener('feed'|'activity')` in `openStream`; guards `isFeedSnapshot`/
`isActivityEvent`; calls `fetchFeed()` (`GET /api/feed`), `postFeedSeen(lastId)`
(`POST /api/feed/seen`), `acceptTicket(id)` (`POST /api/tickets/{id}/accept`). Add
`fanOutFeed`/`fanOutActivity` in `stream-connection.ts`.

Stores (three-file pattern, `useState` + wholesale replace):
- **feed store**: latest `FeedSnapshot` (fetch on mount + replace on `feed` SSE). Session-held
  seen-set: track update `notification_id`s already rendered; on render of unseen updates on
  a visible screen, call `postFeedSeen(maxId)` **only when `document.visibilityState==='visible'`**.
- **activity store** (SSE-only): `thinking: boolean`; `pill: {kind:'say'|'toast', ...} | null`
  + a toast queue. Contention: a `say` replaces any toast and is persistent until the next
  utterance/dismiss; toasts queue behind an active say and auto-dismiss ~4s; `thinking`
  renders only when `pill` is empty. `say` arrives on the existing `say` SSE (reuse onSay).

**Selector conventions (MUST match — the E2E tests assert exactly these):**
- Feed region: `getByRole('region', { name: 'Feed' })` → element also `data-role="feed"`
  carrying `data-connection-state` = connected/reconnecting (mirror the board's gate).
- Header status line: `data-role="feed-status"` (text).
- Feed card: `data-role="feed-card"` with `data-kind` ∈ `blocker|proposal|update|preview`.
  Card label `data-role="feed-card-label"`, body `data-role="feed-card-body"`, preview
  image `data-role="feed-card-image"`. Blocker card must sort first (pinned on top).
- Proposal Accept: a real button `getByRole('button', { name: 'Accept' })`, also
  `data-role="proposal-accept"`.
- "While you were away" divider: `data-role="feed-divider"`.
- All-clear empty state: `data-role="feed-empty"`.
- Activity row: `data-role="activity-row"`; thinking `data-role="thinking-indicator"`;
  toast pill `data-role="toast-pill"` (with `data-verb`); say pill `data-role="say-pill"`.
- Dock (visual shell only, no mic): `data-role="dock"`; states via `data-dock-state` =
  `idle|listening|transcribing`. Tap-to-talk button `getByRole('button', { name: 'Talk' })`.

Design language (from `Kiln Voice Screen.html`): fonts Space Grotesk / IBM Plex Mono /
Instrument Serif via a `<link>` in `index.html`; ember red `oklch(0.55 0.2 26)`; warm paper
bg `radial-gradient(...oklch(0.972 0.013 32)...)`; phone-frame not needed in-app (that was
the mockup canvas) — render full-viewport mobile-first. CSS in a new stylesheet keyed off
`data-*`, imported by `PrimaryScreen`. Reference: `scratchpad/08-design-reference.html`
states 4a (blocker+while-away), 4b (updates), 4c (preview), 4d (all-clear), 5a (live
transcript), 6a (thinking), 6b (toast) + say-reply pill.

DOM-snapshot tests (`toMatchSnapshot()`): 4a/4b/4c/4d feed states, 6a/6b/say pill,
proposal card. Store unit tests: feed session-hold across acks + seen-only-when-visible;
activity contention + auto-dismiss. Fixtures: add `makeFeedSnapshot`, `makeFeedCard`,
`makeActivityEvent` to `src/test/fixtures.ts`.

## G. E2E (`tests/tests`) — Agent: ME (author + own)
Three Playwright specs (below). Run with `AGENT_MODE=mock` +
`KILN_BRAIN_MODEL=claude-haiku-4-5-20251001`. Assert on `/` DOM via the selectors in §F and
`GET /api/feed`. Deterministic preconditions via the §E dev seams; unique-tag isolation;
`expect.poll`, never sleeps.
