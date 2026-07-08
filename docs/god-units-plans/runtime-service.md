# God-unit split plan: `internal/runtime/service.go`

**Target:** `backend/internal/runtime/service.go` (813 LOC)
**Source finding:** architecture audit 2026-07-08, §3.3 ("God units")
**Scope:** single phase, no rollout plan.

---

## 1. The problem

`runtime.Service` is one struct that owns **14 ports** and a 14-positional-arg
`NewService` (`service.go:138-188`), whose doc comment even prescribes an
"append new ports at the end" workaround — a smell that the constructor has
stopped being designed and started being accreted. The one type is
simultaneously six things:

| # | Responsibility          | Methods                                                                                                         | Ports it uses |
|---|-------------------------|----------------------------------------------------------------------------------------------------------------|---------------|
| 1 | Event dispatcher        | `EnqueueEvent`, `Workers`, `handleEvent`, `deadLetterEvent`, `nudgeEvents`                                      | `store`, `brains` |
| 2 | Outbox router           | `handleOutbox`, `deadLetterOutbox`, `blockOnDeliveryFailure`, `notifyOwner`, `wrapOutbox`                       | `store`, `agents`, `puller`, `blocker`, `notifier`, `pusher`, `owner` |
| 3 | Transcript facade       | `PostMessage`, `Say`, `Recent`                                                                                  | `messages`, `sayer` |
| 4 | Feed assembler          | `Feed`, `FeedHistory`, `notificationToCard`                                                                     | `boardReader`, `notifications` (read) |
| 5 | Notification CRUD facade| `PostNotification`, `PostPoke`, `RetractNotification`, `DismissNotification`, `DismissAllNotifications`, `EditNotification`, `ListNotifications`, `MarkSeen` | `notifications` (write) |
| 6 | Push coordinator        | `handleFeedUpdated`, `handleActivityToast`, `handleFeedCompletion`, `pushFeedUpdateNotification`, `pushThinking` + the `*Payload` mirror types | `feedPusher`, `activityPusher`, `pusher`, `notifications`, `notifier`, `owner` |

Everything a caller wants (post a message, read the feed, drain the queue,
mutate a notification) forces a dependency on the whole 14-port object. Tests
build the entire graph to exercise one path — `feed_test.go` calls the 14-arg
`NewService` ten times. The struct has no internal cohesion; it is a namespace,
not a unit.

**Good news:** the *port interfaces* are already factored into their own files
(`store.go`, `transcript.go`, `notifications.go`, `feed.go`, `queue.go`). Only
the **`Service` struct and its methods** are monolithic. The split is therefore
a re-homing of methods and a re-wiring of the composition root — not a
redesign of the contracts.

---

## 2. Target decomposition

Six focused types, each owning a coherent slice of ports. Names are `runtime`
package-level (same package — see §5 for why not sub-packages).

```
                       ┌────────────────────────────────────────────┐
                       │            Dispatcher  (queue core)          │
                       │  EnqueueEvent · Workers · handleEvent        │
                       │  handleOutbox · deadLetter{Event,Outbox}     │
                       │  ports: Store, BrainResolver, Puller,        │
                       │         Blocker, AgentRuntime                │
                       └───┬───────────────┬───────────────┬─────────┘
              Say / nudge  │      notify.send │       UI topics │
                           ▼                 ▼                 ▼
                   ┌──────────────┐   ┌────────────┐   ┌───────────────────┐
                   │  Transcript  │   │   Notify   │   │      FanOut        │
                   │ PostMessage  │   │ owner→send │   │ handleFeedUpdated  │
                   │ Say · Recent │   └─────┬──────┘   │ handleActivityToast│
                   │ ports:       │         │          │ handleFeedCompletion│
                   │  MessageStore│         │          │ pushThinking       │
                   │  SayPusher   │         │          │ ports: Snapshot/   │
                   └──────────────┘         │          │  Feed/ActivityPusher│
                                            │          │  NotificationWriter │
                                            └──────────┤  + Feed + Notify    │
                                                       └─────────┬──────────┘
                                                          assemble │
                                                                   ▼
                       ┌──────────────┐                    ┌──────────────┐
                       │Notifications │                    │     Feed     │
                       │ Post/Retract │                    │ Feed         │
                       │ Dismiss/Edit │                    │ FeedHistory  │
                       │ List/MarkSeen│                    │ ports:       │
                       │ ports:       │                    │  BoardReader │
                       │ Notification-│                    │  Notification│
                       │  Store       │                    │  Reader      │
                       └──────────────┘                    └──────────────┘
```

### 2.1 `Transcript` — the conversation surface
- **Ports:** `MessageStore`, `SayPusher`, plus a `Nudger` hook (see §4).
- **Methods:** `PostMessage`, `Say`, `Recent`.
- Backs api `MessagePoster` + `MessagesReader`, brain `Say` + `ConversationReader`.

### 2.2 `Notifications` — notification CRUD
- **Ports:** `NotificationStore` (writer + reader; `List*` needs the read half).
- **Methods:** `PostNotification`, `PostPoke`, `RetractNotification`,
  `DismissNotification`, `DismissAllNotifications`, `EditNotification`,
  `ListNotifications`, `MarkSeen`.
- Backs api `FeedMutator`, brain `NotificationStore` + `FeedReader`, steward `PostPoke`.

### 2.3 `Feed` — the feed assembler
- **Ports:** `BoardReader`, `NotificationReader` (read-only — reuse the existing
  split half of `NotificationStore`).
- **Methods:** `Feed`, `FeedHistory`; owns `notificationToCard`, `feedPageSize`.
- Backs api `FeedReader`. Pure read/assembly, zero mutation.

### 2.4 `Notify` — the tenant-scoped push choke point
- **Ports:** `Owner`, `Notifier`.
- **Method:** `Send(ctx, projectID, payload)` — the current `notifyOwner`:
  resolve owner → send, enforcing the tenant boundary (11 §3) in one place.
- Used by both `Dispatcher` (the `notify.send` topic) and `FanOut` (the
  transition push). Extracting it kills the only dependency two other units
  would otherwise share.

### 2.5 `FanOut` — the push / SSE coordinator
- **Ports:** `SnapshotPusher`, `FeedPusher`, `ActivityPusher`,
  `NotificationWriter` (only for `PostCompletionCard`).
- **Collaborators:** `Feed` (to assemble a snapshot), `Notify` (transition push).
- **Methods:** `PushThinking`, `PushBoard` (delegates `board.updated`),
  `handleFeedUpdated`, `handleActivityToast`, `handleFeedCompletion`,
  `pushFeedUpdateNotification`; owns `feedUpdateNotification`,
  `feedUpdateVerbBody`, and the `notifyPayload` / `toastPayload` /
  `feedUpdatedPayload` / `completionPayload` mirror structs.
- This is where **all SSE fan-out and every self-healing UI-topic handler**
  lives. Its methods are the ones with "logs-and-drops" semantics; isolating
  them makes that best-effort contract legible in one file.

### 2.6 `Dispatcher` — the durable queue core
- **Ports:** `Store`, `BrainResolver`, `Puller`, `Blocker`, `AgentRuntime`.
- **Collaborators:** `Transcript` (`Say` for the system-error / brain-unresolved
  replies), `Notify` (`notify.send`), `FanOut` (the four UI topics +
  `pushThinking` bracket).
- **Methods:** `EnqueueEvent`, `Workers`, `handleEvent`, `deadLetterEvent`,
  `handleOutbox`, `deadLetterOutbox`, `blockOnDeliveryFailure`, `NudgeEvents`;
  owns the topic-name consts, `errUnknownTopic`, `systemErrorMessage`,
  `brainUnavailableMessage`, `wrapOutbox`.
- This is the module's spine — the deploy-resumable drain (04 §5). It routes,
  but no longer *implements* the feed/notification/transcript work it routes to.

### Dependency count, before vs after
| Type          | Ports | Collaborators | vs. today |
|---------------|-------|---------------|-----------|
| `Transcript`  | 2     | 1 (Nudger)    | was 14    |
| `Notifications`| 1    | 0             | was 14    |
| `Feed`        | 2     | 0             | was 14    |
| `Notify`      | 2     | 0             | was 14    |
| `FanOut`      | 4     | 2             | was 14    |
| `Dispatcher`  | 5     | 3             | was 14    |

No single unit exceeds 5 ports; the four leaf services (`Transcript`,
`Notifications`, `Feed`, `Notify`) each take ≤2 and are trivially testable in
isolation.

---

## 3. New file structure

The port/type files stay as-is. `service.go` is deleted and its contents
re-homed:

```
internal/runtime/
  doc.go                 (unchanged)
  queue.go               (unchanged — Entry, Event, retry consts)
  store.go               (unchanged — Store, Clock)
  transcript.go          (unchanged — Message, MessageStore, SayPusher)   ── types/ports
  notifications.go       (unchanged — Notification, NotificationStore)    ──
  feed.go                (+ move SnapshotPusher here, co-located with     ──
                            FeedPusher/ActivityPusher; types unchanged)
  worker.go              (unchanged — Worker)

  dispatcher.go     NEW  Dispatcher + Brain/BrainResolver/Puller/Blocker/
                         AgentRuntime ports + topic consts + errUnknownTopic
                         + system messages + wrapOutbox
  transcript_service.go  NEW  Transcript + Nudger
  notification_service.go NEW Notifications
  feed_assembler.go NEW  Feed methods (Feed, FeedHistory, notificationToCard,
                         feedPageSize)
  fanout.go         NEW  FanOut + the four *Payload mirror structs +
                         feedUpdateNotification + feedUpdateVerbBody +
                         completionCardBody
  notify.go         NEW  Notify + Owner + Notifier ports

  (service.go            DELETED)
```

Test files split to match (see §7): `service_test.go` → `dispatcher_test.go`,
plus `feed_test.go` (already feed-focused) targets `Feed`/`FanOut` directly.

Method-move map (all bodies move verbatim; only the receiver changes):

| From `service.go`                       | To            | Receiver becomes |
|-----------------------------------------|---------------|------------------|
| `EnqueueEvent`, `Workers`, `handleEvent`, `deadLetterEvent`, `handleOutbox`, `deadLetterOutbox`, `blockOnDeliveryFailure`, `nudgeEvents` | `dispatcher.go` | `*Dispatcher` |
| `PostMessage`, `Say`, `Recent`          | `transcript_service.go` | `*Transcript` |
| `PostNotification`, `PostPoke`, `Retract…`, `Dismiss…`, `DismissAll…`, `Edit…`, `ListNotifications`, `MarkSeen` | `notification_service.go` | `*Notifications` |
| `Feed`, `FeedHistory`, `notificationToCard` | `feed_assembler.go` | `*Feed` |
| `notifyOwner`                           | `notify.go`   | `*Notify` (renamed `Send`) |
| `handleFeedUpdated`, `handleActivityToast`, `handleFeedCompletion`, `pushFeedUpdateNotification`, `pushThinking`, `feedUpdateNotification` | `fanout.go` | `*FanOut` |

---

## 4. Breaking the one cycle

`Transcript.PostMessage` must nudge the events worker, but the worker is built
and owned by `Dispatcher.Workers`. Meanwhile `Dispatcher.handleEvent` calls
`Transcript.Say`. Naïvely that is a construction cycle.

Break it with a one-method hook, mirroring today's already-nil-safe
`nudgeEvents`:

```go
// Nudger wakes the events worker after an ingest. Nil-safe before Workers()
// runs (the poll fallback still catches the row), exactly like today's
// nudgeEvents guard.
type Nudger interface{ NudgeEvents() }
```

- `Dispatcher` satisfies `Nudger` (its `NudgeEvents` wraps `eventsWorker.Nudge`).
- `Transcript` holds a `nudge Nudger` field, injected **after** both are built
  (the composition root calls `transcript.SetNudger(dispatcher)` — or the field
  is assigned in wiring). Because Go interfaces are structural and both live in
  one package, this is a plain field set, no new import.
- `Dispatcher` depends on `Transcript` through its concrete type (or a tiny
  `Sayer` interface) — a one-way edge. The reverse edge is the `Nudger`
  interface, so there is no compile-time or construction cycle.

`EnqueueEvent` stays on `Dispatcher` (it already holds the worker); only
`PostMessage`'s nudge routes through the hook.

---

## 5. Why same-package, not sub-packages

Keep all six types in `package runtime`:

- The port interfaces and domain types (`Entry`, `Message`, `Notification`,
  `FeedSnapshot`, …) are shared across the six units. Sub-packaging would force
  either a shared `runtimetypes` package or a web of cross-imports, and risk an
  import cycle (`Dispatcher` → `FanOut` → `Feed` → types → …).
- Nothing outside the package needs the split to be visible: consumers already
  bind to **role interfaces** (`api.MessagePoster`, `brain.Say`, etc.), not to
  `*runtime.Service`. Splitting the concrete types behind the same interfaces is
  invisible to them.
- Same-package means **zero export/visibility churn** and no new module
  boundary to police.

Sub-packages remain a possible *later* step if a unit grows its own sub-tree;
this plan deliberately does not take it.

---

## 6. Consumer & composition-root impact

No consumer's *interface* changes — only which concrete value the composition
root hands each slot. `cmd/kiln/wiring.go:237` stops calling the 14-arg
`NewService` and instead builds six values:

```go
notify := runtime.NewNotify(newNotifier(cfg, pushStore, owner, log), owner)
feed   := runtime.NewFeed(&boardViewAdapter{inner: boardSvc}, runtimepg.New(db))
notifs := runtime.NewNotifications(runtimepg.New(db))
tx     := runtime.NewTranscript(runtimepg.New(db), hub /*SayPusher*/)
fanout := runtime.NewFanOut(hub, hub, hub /*Snapshot/Feed/Activity*/, runtimepg.New(db), feed, notify)
disp   := runtime.NewDispatcher(runtimepg.New(db), &brainResolver{…}, boardSvc,
             &blockerAdapter{…}, &agentRuntimeAdapter{…}, tx, notify, fanout)
tx.SetNudger(disp)                       // close the ingest→nudge edge (§4)
```

Slot re-wiring:

| Consumer slot (today: `rtSvc`)                     | New value        |
|----------------------------------------------------|------------------|
| `api.NewServer` `poster MessagePoster`             | `tx`             |
| `api.NewServer` `messages MessagesReader`          | `tx`             |
| `api.NewServer` `feed FeedReader`                  | `feed`           |
| `api.NewServer` `seen FeedMutator`                 | `notifs`         |
| `agentEventAdapter.rt` (agent→runtime `EnqueueEvent`) | `disp`        |
| `sayAdapter.rt` (brain `Say`)                      | `tx`             |
| `convoAdapter.rt` (brain `ConversationReader`)     | `tx`             |
| `notificationsAdapter.rt` (brain `NotificationStore`) | `notifs`      |
| `feedReaderAdapter.rt` (brain `FeedReader`)        | `notifs`         |
| `stewardFeedAdapter.inner` (`PostPoke`)            | `notifs`         |
| dev `NotificationPoster` (`PostNotification`)      | `notifs`         |
| `rtSvc.Workers(clock)`                             | `disp.Workers`   |
| `agentEvents.rt = rtSvc` cycle close               | `agentEvents.rt = disp` |

The adapters in `cmd/kiln/adapters.go` change only the *type* of their embedded
field (`rt *runtime.Service` → `rt *runtime.Transcript` / `*runtime.Notifications`
/ `*runtime.Dispatcher`); their bodies are untouched. This is the payoff: each
adapter now advertises exactly which slice of the runtime it needs.

**Constructor style (audit's fix):** each focused constructor takes ≤6
positional args and is self-documenting. `NewDispatcher` (5 ports + 3
collaborators = 8) is the one worth a `Deps`/`Ports` struct if it reads poorly;
otherwise positional is fine. No constructor carries the "append at the end"
comment forward.

**Wire schema:** none of this touches the client↔server contract (`/schema`),
so **no regen** is required — this is a pure internal refactor. Call it out in
the PR so a reviewer doesn't go looking.

---

## 7. Test impact

- `feed_test.go` (10× `NewService`) → construct `Feed` (assembly assertions)
  and `FanOut` (push/handler assertions) directly. Most feed tests only ever
  exercised those two concerns through the god object; they get *simpler*.
- `service_test.go` → rename to `dispatcher_test.go`; construct `Dispatcher`
  with a fake `Transcript`/`Notify`/`FanOut` (or the real leaf services over
  fakes). The event-drain and outbox-routing assertions live here.
- `degradation_test.go` → targets `Dispatcher` (self-healing drain) and/or
  `FanOut` (best-effort push drops).
- `fakes_test.go` — unchanged; the fakes implement the same ports.
- `tenancy_integration_test.go` (`internal/api`) → update its one `NewService`
  call to build the six values (or a small test helper that does).

A tiny `internal/runtime` test helper (`buildRuntime(t, fakes) (…)`) that
returns the wired sextet keeps the per-test churn to one line each.

---

## 8. Implementation strategy (single phase, ordered for a green build at each step)

Order chosen so each step compiles and the hard gate stays green; leaf services
first, spine last, delete last.

1. **`Notify`** — extract `notifyOwner`→`Send`, move `Owner`/`Notifier` ports to
   `notify.go`. Have `Service` delegate to an embedded `*Notify` so nothing
   else moves yet.
2. **`Feed`** — move `Feed`/`FeedHistory`/`notificationToCard`/`feedPageSize`
   into `feed_assembler.go` on `*Feed`; `Service` delegates.
3. **`Notifications`** — move the eight CRUD methods to
   `notification_service.go`; `Service` delegates.
4. **`Transcript`** — move `PostMessage`/`Say`/`Recent`; add `Nudger` hook;
   `Service` delegates and passes itself as the nudger for now.
5. **`FanOut`** — move the four UI handlers + `pushThinking` + push helpers +
   the `*Payload` mirror types; wire it over the `Feed`/`Notify`/`NotificationWriter`
   built above; `Service` delegates. Move `SnapshotPusher` into `feed.go`.
6. **`Dispatcher`** — move `EnqueueEvent`/`Workers`/`handleEvent`/`handleOutbox`/
   dead-letter paths + topic consts into `dispatcher.go` on `*Dispatcher`,
   collaborating with the `Transcript`/`Notify`/`FanOut` values. At this point
   `Service` is a thin aggregate that only holds the six and forwards.
7. **Flip the composition root** (`wiring.go`, `adapters.go`) and the api/brain/
   steward slots to the six concrete values per §6; wire `tx.SetNudger(disp)`.
8. **Flip the tests** (§7) to build the focused types.
9. **Delete** `Service`, `NewService`, and `service.go`.
10. **Hard gate** (per `end-to-end-development`): `gofmt`/lint, `go vet`/type
    check, and tests at all three levels (unit, module, integration incl.
    `tenancy_integration_test.go`). Confirm no `/schema` regen needed.

Steps 1–6 keep `Service` as a delegating shim so the tree compiles and tests
pass after **every** step — the risky "big bang" is avoided even though it is
one phase. Steps 7–9 remove the shim once all callers are moved.

---

## 9. Benefits recap

- **Constructor rot fixed.** The 14-arg `NewService` and its "append at the end"
  comment are gone; each unit's constructor names exactly what it needs.
- **Testability.** `Feed`, `Notify`, `Notifications`, `Transcript` are each
  buildable over 1–2 fakes; feed tests stop constructing an event dispatcher and
  a push coordinator they never call.
- **Legible contracts.** The best-effort "logs-and-drops" push code (`FanOut`)
  is separated from the durable, retry-bearing drain (`Dispatcher`) and from the
  transactional notification writes (`Notifications`) — three very different
  failure-handling disciplines that today sit interleaved in one file.
- **Tenant boundary in one place.** `Notify` is the single owner-resolution
  choke point, reused by both callers instead of a private method two
  responsibilities happen to share.
- **Sharper consumer coupling.** Every `cmd/kiln` adapter now embeds the
  narrowest runtime type it needs, so the dependency arrows in wiring say what
  they mean.
- **Localized payload mirrors.** The four board-mirrored `*Payload` structs the
  audit flags (§3.3 Medium) collapse into `fanout.go`, the one file that decodes
  them — reducing the surface if the "shared kernel vs. mirror" decision is
  revisited later.

## 10. Non-goals

- No change to any port interface, wire schema, or DB layer (`./postgres`).
- No sub-packaging (§5).
- The audit's separate "mirror board structs vs. shared kernel" decision (§3.3
  Medium) is **not** resolved here — only relocated. Left for a follow-up.
- No behavioral change: this is a pure re-homing of methods behind unchanged
  role interfaces.
