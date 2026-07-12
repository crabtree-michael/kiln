# Design: server-side push dedup via foreground presence

**Date:** 2026-07-12
**Status:** proposed
**Scope:** `internal/push`, `internal/api`, `cmd/kiln`, `/schema`, `/frontend`. New DB
migration (`internal/push/postgres`). Wire-schema change (regen both sides).
**Spec anchors:** 02 §10 (notification transport), 04 §7 (SSE hub), 08 §4 (activity
resync), 11 §3 (per-user routing).

> **Where this lives.** The task brief said `docs/spec`; Kiln keeps design proposals in
> `docs/superpowers/specs/<date>-<slug>-design.md` (every prior design doc is here), so this
> follows that convention. The numbered `docs/specs/NN-*.md` are the standing architecture;
> this proposal amends **02 §10** and should be cross-referenced there once landed.

## Problem

Apple's Web Push (iOS/iPadOS 16.4+ Home Screen web apps, macOS Safari 16+) enforces the
`userVisibleOnly` contract **with no tolerance**: a service-worker `push` handler that finishes
without calling `showNotification()` is treated as a silent push, and WebKit **permanently
revokes the push subscription after a small budget (~3) of them** — notifications simply "turn
off" with no user action. (This is already documented, and worked around, in
`frontend/public/push-sw.js:61-75`.)

That collides with our in-app UX. When a Kiln tab is **open and foregrounded**, a ticket moving
to Blocked already surfaces in the live board (the Blocked zone, 07 §6) and as an in-app toast
over SSE (08 §4). If we also deliver a Web Push for the same event, the user gets a **duplicate**:
an OS banner on top of the toast they can already see.

Today we dedup **in the service worker** (`push-sw.js:56-59,86`): if a Kiln tab is foregrounded,
the `push` handler returns without showing. **This is safe only on Chromium/Gecko**, which grant a
silent-push budget. On **iOS it is explicitly disabled** (`isAppleWebKit()` short-circuit,
`push-sw.js:86`) precisely because skipping the notification there costs the whole subscription.
So on iOS — the platform this feature exists for — **the duplicate is unavoidable at the client**.

**The only iOS-safe place to dedup is the server, by not sending the push at all.** The
`userVisibleOnly` penalty attaches to a push that is *received and not shown*. A push that is
**never sent** is received by nothing, shows nothing, and costs no budget. So the fix is: the
backend learns when a user has the app foregrounded, and **withholds the push for that device**,
letting the in-app toast be the only surface.

### The reliability constraint (this drives every decision)

**A missed notification (false negative) is far worse than a duplicate (false positive).** A
Blocked ticket that never reaches the user stalls the whole orchestration; a redundant banner is a
minor annoyance. So the system must be built to **suppress only on positive, fresh evidence that
the app is foregrounded, and to *send* on any doubt** — stale data, missing data, tracking errors,
races, crashes, and network loss all resolve to *send*. Server-side dedup is a best-effort
optimization layered over a send-by-default core; it must never become a gate that can silently
eat a notification.

## Research

> Findings gathered 2026-07-12; primary sources (WebKit, W3C, MDN, RFCs) cited inline. See
> [Research appendix](#research-appendix) for the full claim/citation table.

**R1 — WebKit requires a visible notification for every *received* push, and revokes the
subscription when you don't.** WebKit's own docs state it plainly: you must set `userVisibleOnly:
true` and "fulfill that promise by always showing a notification in response to a push message…
Violations of the `userVisibleOnly` promise will result in a push subscription being revoked"
([Meet Web Push, webkit.org](https://webkit.org/blog/12945/meet-web-push/)). The **exact budget
is undocumented**: third-party reports and Apple Developer Forum threads consistently put it at
**~3 silent pushes** (a `push` handler that terminates before `showNotification()` displays —
notably, omitting `event.waitUntil()` counts as silent even if a notification shows shortly
after), but that count appears in *no* official Apple/WebKit source and should be treated as an
empirically-observed threshold that could change. The **mechanism** (received-but-not-shown →
revocation) is official; the **number** is folklore. WebKit is far stricter here than
Chromium/Gecko, so **client-side foreground suppression is not viable on iOS** — confirmed by our
own worker's `isAppleWebKit()` carve-out (`push-sw.js:61-75`).

**R2 — The penalty is for *received-but-not-shown*, not for *not-sending*.** The revocation rule is
defined as a violation of the promise to "always show a notification **in response to a push
message**" — so no push message received ⇒ no `push` event fires ⇒ no promise to violate. A server
that simply never sends does nothing WebKit can observe. **Therefore server-side withholding is
penalty-free.** This is a direct *inference* from the documented rule (no primary source spells out
"not-sending is safe" verbatim), but it is the standard industry reading and is logically entailed
by a budget that only counts *delivered* pushes. It is the load-bearing fact for the whole design —
called out explicitly so a future reader does not "optimize" it back into the worker.

**R3 — Foreground detection: the Page Visibility API is the right signal, but its "leaving"
events are unreliable.** `document.visibilityState` / `visibilitychange` reliably tell a *running,
scheduled* page whether it is visible. But the events that fire when a page *goes away* —
`pagehide`, `freeze`, and especially `beforeunload`/`unload` — are **not guaranteed**: an OS
process kill, a tab discard, a crash, or an abrupt loss of connectivity fires none of them. On iOS
specifically, backgrounding a Home Screen web app freezes the page (Page Lifecycle `freeze`) and
may or may not deliver a clean `pagehide` before suspension. **Takeaway: we can trust "I am
visible" as a positive assertion while it keeps arriving; we must NOT trust the absence of a
"going away" signal to mean the app is still open.** This is exactly why the design leans on a
short-lived, self-expiring lease rather than an explicit online/offline flag.

**R4 — An SSE/WebSocket connection proves a *tab exists*, not that it is *foregrounded*.** A
backgrounded, frozen iOS PWA can hold its SSE socket open (or have it linger half-dead) while
showing nothing to the user. So the existing hub connection (04 §7) is **necessary context but not
a sufficient foreground signal** — the design uses an explicit visibility heartbeat as the
authoritative presence signal, not raw connection liveness.

**R5 — A push server cannot query device liveness.** RFC 8030 gives no "is this endpoint online"
call; the server only learns a subscription is *dead* lazily, via `404`/`410` on the next send
(which we already prune on, `sender.go:88`). So presence must be **reported by the client**, not
probed by the server.

**R6 — The documented prior art is Discord: server-side suppression gated on an idle timeout.**
Discord's backend withholds mobile push while the user is active on another client, and only routes
to push once they've been idle past a **configurable "Push Notification Inactive/AFK Timeout"**
(default historically ~2 min; Discord suggests ~30s) — i.e. a server-side, presence-driven decision
on a recency window, exactly the lease shape below. (Slack, WhatsApp Web, and Linear are *widely
believed* to do the same but I could **not** find primary documentation — treat only Discord as
confirmed; see appendix.) Note Discord biases toward *suppression* (a generous timeout so it doesn't
over-notify); Kiln inverts that bias toward *sending* (a short TTL), because our failure cost is a
missed Blocked ticket, not an extra buzz.

## Approach

Three pieces: a **client foreground heartbeat**, a **per-device presence lease** on the server, and
a **suppression check inside the existing per-subscription send loop**. All three are designed so
that the *only* behavior change versus today is "sometimes we skip a send"; every failure path
falls back to the current always-send behavior.

```
 frontend                         backend (api)              backend (push)
 ────────                         ─────────────              ──────────────
 visible → POST /api/presence ─▶  stamp last_seen_fg_at
   (heartbeat every ~15s,          on the caller's
    + once on becoming             subscription row
    visible)                       (scoped to session user)

 hidden → sendBeacon              clear last_seen_fg_at
   /api/presence {visible:false}   (best-effort)

                                  notify.send ──▶ Sender.Send(user)
                                                    for each subscription:
                                                      fresh lease?  → SKIP (log)
                                                      else / error  → SEND  ◀── default
```

### 1. Foreground presence signal (client → backend)

A new `usePresence` hook, mounted once behind the app shell, that reports **only the positive fact
"this device is visible"** on a short cadence:

- **Heartbeat.** While `document.visibilityState === 'visible'`, `POST /api/presence
  { visible: true }` immediately on becoming visible and then every **`HEARTBEAT = 15s`** on a
  timer. It reuses the existing `visibilitychange` infrastructure already present in
  `activity-store.tsx:180` and `feed-store.tsx:467`.
- **Leave beacon (best-effort optimization only).** On `visibilitychange → hidden` and on
  `pagehide`, fire `navigator.sendBeacon('/api/presence', {visible:false})`. `sendBeacon` is the
  one send that survives the page being torn down. Per **R3** this is treated as an *optimization*
  that collapses the dedup window on the common, clean background — **never** as a correctness
  requirement, because it may not fire.
- **What it carries.** The device's own push-subscription endpoint (from
  `pushManager.getSubscription()`), so the server can stamp *that device's* row and suppression is
  per-device (see §3, multi-device). If the device has no subscription (notifications off), the
  hook still no-ops usefully — there is no push to suppress, so it need not send at all.
- **Cost.** One tiny authenticated POST every 15s while visible. This is cheaper than, and aligned
  with, the SSE keepalive cadence (`keepaliveInterval = 25s`, `hub.go:20`); a hidden tab sends
  nothing.

Why a separate uplink and not "reuse the SSE stream": SSE is server→client only — the client
cannot push visibility up it — and per **R4** the connection is not the signal anyway.

### 2. Presence lease (server state)

Presence is stored **on the subscription row**, not in a new table — presence is inherently
per-device and the endpoint is already the device's unique key (`push_subscriptions.endpoint
UNIQUE`), so co-locating them makes correlation free and pruning automatic (a dead endpoint's
presence dies with its row).

Migration `internal/push/postgres/migrations/0004_presence.sql`:

```sql
ALTER TABLE push_subscriptions
  ADD COLUMN last_seen_foreground_at timestamptz;  -- NULL = never/not foreground
```

`POST /api/presence` (new handler in `internal/api`, session-authenticated like the other
`/api/push/*` routes):

- `{visible:true, endpoint}` → set `last_seen_foreground_at = now()` for the row **matching that
  endpoint AND owned by the session user** (the same user-scoped guard as `DeleteUserEndpoint`,
  `push.go:57`, so one user can never stamp another's device). `now()` is **server clock** — the
  client never sends a timestamp, sidestepping clock skew entirely.
- `{visible:false, endpoint}` → set `last_seen_foreground_at = NULL` for that row (clears the lease
  immediately on clean background).
- Endpoint not found / not owned → **no-op, 204**. A presence beacon that races ahead of the
  subscription (or references a rotated endpoint) simply stamps nothing, and that device keeps
  receiving pushes (fail-open).

The store (`push.Store`) gains `TouchForeground(ctx, userID, endpoint string, visible bool)`, and
`List` returns `last_seen_foreground_at` alongside each `Subscription` so the sender can decide
without a second query.

### 3. Suppression decision (server, at send time)

The decision lives exactly where the send already fans out per device — `Sender.sendOne`
(`sender.go:66`), guarded once per subscription inside the existing loop (`sender.go:58`). **Per
subscription** (not per user) so multi-device works: a phone that is backgrounded still gets the
push even while the laptop that is foregrounded is skipped.

```go
const presenceTTL = 30 * time.Second // ≈ 2× HEARTBEAT + slack; short on purpose (see below)

func (s *Sender) foregrounded(sub Subscription, now time.Time) bool {
    return sub.LastSeenForegroundAt != nil &&
        now.Sub(*sub.LastSeenForegroundAt) < presenceTTL
}
```

In `Send`'s loop: `if s.foregrounded(sub, s.now()) { log & continue }` else `sendOne(...)` exactly
as today. The skip is **logged** (endpoint + age) so suppression is observable and auditable —
never silent (matches the "no silent caps" house rule).

**Why the TTL is short, not generous.** The TTL is the *safety window for the crash path* (R3):
when the leave-beacon does not fire, a backgrounded device's lease must expire fast so we resume
sending. A short TTL means *at most `presenceTTL`* of exposure where a real notification could be
withheld from a device that has actually gone away — after that we send. The cost of "too short" is
merely a duplicate (we send while genuinely foreground); the cost of "too long" is a **missed
notification**. Per the reliability constraint we bias to short. `30s` (≈ two heartbeats + network
slack) tolerates one dropped beacon without a false-miss and bounds the crash-path exposure to half
a minute. On the *clean* background path the leave-beacon nulls the lease immediately, so the TTL
never even applies there.

Common presence systems run a ~30s heartbeat with a **60–90s** staleness window (R6/appendix). We
deliberately pick the **short** end of that range because those systems optimize against
*over-notifying*, whereas Kiln optimizes against *missing* — the asymmetry is inverted, so our
staleness window is inverted with it. `HEARTBEAT` and `presenceTTL` are single server/client
constants; if duplicates on the foreground race prove annoying in practice they can be tuned up
without touching the fail-open structure.

**Fail-open is structural, not incidental:**
- `List` / presence read error → the sender already returns the error up and the outbox retries;
  but the per-subscription check treats a `nil` / absent `last_seen_foreground_at` as **not
  foreground → send**. There is no code path where "we couldn't tell" resolves to suppress.
- No presence ever reported (older subscription, presence endpoint down, hook not mounted) →
  column is `NULL` → send. The feature degrades to exactly today's behavior.
- Suppression is confined to the send decision; it never touches subscription pruning, mode
  gating, or outbox completion.

### 4. Interaction with the existing service-worker dedup

The SW-side foreground check (`push-sw.js:86`) **stays as-is**:

- On **iOS** it is already a no-op (always shows). Server suppression is now what prevents the
  duplicate there — and because the push is never sent, the worker never runs for the suppressed
  event, so there is no budget cost. This is the whole point.
- On **Chromium/Gecko** the SW check remains a cheap **secondary guard** for the unavoidable race
  in §Edge cases (a push already in flight when the app foregrounds, or in flight during the
  sub-TTL window). Server + SW are redundant here but strictly reduce duplicates; both are safe on
  these engines. No change required.

## Edge cases and failure handling

| Scenario | Presence state | Outcome | Why it's correct |
|---|---|---|---|
| App cleanly backgrounded (visibilitychange→hidden) | leave-beacon nulls lease | **send** | Immediate, no TTL wait. |
| App crash / OS kill / tab discarded | no beacon; lease ages out ≤ `presenceTTL` | **send after ≤30s** | R3: can't trust absence of a leave signal; short TTL bounds the miss window. |
| Device goes offline while foregrounded | heartbeats stop; lease ages out | **send** | Push service queues it (TTL `3h`, `sender.go:16`); delivered on reconnect — better than suppressing. |
| Notification fires in the gap between background and lease expiry | lease still fresh | **suppress → potential miss** | The one residual miss risk; bounded to ≤`presenceTTL` and shrunk to ~0 on the clean path by the leave-beacon. Bias-to-short TTL is the mitigation. |
| Notification fires just as app foregrounds, before 1st heartbeat | lease still `NULL`/stale | **send → duplicate** | Accepted false positive (toast + banner). Non-iOS SW check often catches it. |
| Multi-device: phone backgrounded, laptop foregrounded | per-row leases differ | **send to phone, skip laptop** | Per-subscription decision (§3); user still gets the notification on the idle device. |
| Presence endpoint / DB read fails | check can't confirm foreground | **send** | Fail-open by construction (§3); tracking failure never eats a notification. |
| Subscription rotated / presence races subscribe | endpoint doesn't match a row | **send** | Presence no-ops; no fresh lease ⇒ send. |
| Clock skew | server stamps and compares its own clock | **unaffected** | Client sends no timestamps. |
| `notify.send` outbox at-least-once replay | decision re-evaluated | benign | "A rare duplicate notification is accepted as benign" (04 §3); already true today. |
| Cross-tenant | presence write user-scoped; List user-scoped | **isolated** | Same guard as `DeleteUserEndpoint` (11 §3). |

**Net miss exposure:** zero on every path *except* the crash-during-a-live-blocked-event window,
which is bounded above by `presenceTTL` (~30s) and driven toward zero on clean backgrounds by the
leave-beacon. Every other uncertainty resolves to *send*.

## Implementation approach

Ordered so each step is independently shippable and the feature is inert until the last wiring
step (the sender check).

1. **Schema (`/schema/openapi.yaml`).** Add `POST /api/presence` with body
   `{ visible: boolean, endpoint: string }` → `204`. Regenerate Go + TS types (never hand-edit
   generated files — wire-schema rule, 02 §4). No change to the `Notification` payload or the
   `notify.send` outbox contract.
2. **Migration.** `0004_presence.sql` adds the nullable `last_seen_foreground_at` column. Backfills
   nothing (NULL = send, the safe default), so it is a no-downtime additive migration.
3. **`internal/push` store + Sender.** `Store.TouchForeground`; `List`/`Subscription` carry
   `LastSeenForegroundAt *time.Time`; `Sender` gets an injected `now func() time.Time` (for tests)
   and the `foregrounded()` skip in the loop. `Sender` is the only place the decision exists.
4. **`internal/api`.** `POST /api/presence` handler mounted under `s.withSession(...)` exactly like
   `POST /api/push/subscribe` (`routes.go:415`) — the guard hands the handler an `identity.User`, so
   the handler is `func(w, r, user identity.User)`, decodes `{visible, endpoint}`, and calls the
   registrar with `user.ID` (never a client-supplied user, never `withProject` — presence is a
   user/device fact, not a project one). The api package's `PushRegistrar` port (`routes.go:194`)
   gains `TouchForeground(ctx, userID, endpoint string, visible bool) error`, satisfied by
   `push.Store`. Body cap and 400-on-missing-endpoint mirror `handlePushSubscribe` (`routes.go:896`);
   unknown/foreign endpoint is a 204 no-op.
5. **`cmd/kiln`.** No new adapter — `webPushNotifier` already routes through `push.Sender`; wiring
   is just passing a real clock. Behavior is unchanged when the column is all-NULL, so this is safe
   to deploy ahead of the client.
6. **`/frontend`.** `usePresence` hook (heartbeat + leave-beacon) reusing the existing
   `visibilitychange` listeners; mounted behind the authed app shell only. Feature-detect
   `navigator.sendBeacon`; gate the whole hook on the presence of a push subscription.

### Codebase validation (verified 2026-07-12)

Every seam this design names was checked against `main`, so the plan is implementation-ready:

- **Single send path.** `notify.send` → `webPushNotifier.Send` (`cmd/kiln/adapters.go:674`) →
  `push.Sender.Send` (`sender.go:49`), which is the *only* place subscriptions are enumerated
  (`store.List`, `sender.go:54`) and delivered per-device (`sendOne`, `sender.go:66`). Adding the
  skip inside that loop is the whole backend behavior change — nothing else sends.
- **Store shape.** `push_subscriptions(endpoint UNIQUE, …, user_id)` with a user-scoped
  `List`/`DeleteUserEndpoint` (`store.go:52,93`) — the exact scoping guard `TouchForeground` copies.
  Adding a nullable column and returning it from `List` is additive.
- **Auth/scoping.** `withSession` (`session.go:42`) resolves the cookie session to an `identity.User`
  and passes it to the handler; `handlePushSubscribe` (`routes.go:896`) is the template to mirror.
- **Wire regen.** `make schema` runs `openapi-typescript` (→ `frontend/src/schema/generated.ts`) and
  `oapi-codegen` (→ `backend/internal/wire/generated.go`); `make schema-verify` fails on stale
  generated files (`Makefile:101-109`) — so the `/api/presence` addition regenerates both sides and
  is gate-enforced.
- **Client endpoint access.** `use-web-push.ts` already narrows `PushSubscription.toJSON()` to
  `{endpoint, keys}` and reads `registration.pushManager.getSubscription()` (`use-web-push.ts:63-78,
  103`) — `usePresence` reuses that to obtain the device's endpoint; no new browser plumbing.
- **Existing visibility infra.** `activity-store.tsx:180` and `feed-store.tsx:467` already attach
  `visibilitychange` listeners and resync on becoming visible — the heartbeat rides the same event.

### Testing

- **`push.Sender` (`internal/push/sender_test.go`, extend the existing httptest harness):**
  (a) fresh `last_seen_foreground_at` → endpoint is **skipped**, no POST to the test push server;
  (b) stale (older than `presenceTTL`) → **sent**; (c) `NULL` → **sent**; (d) two subscriptions,
  one fresh one stale → exactly the stale one is sent (multi-device); (e) injected `now` drives the
  boundary. Reuse the throwaway-VAPID + `httptest` pattern already there.
- **`push.Store` (postgres):** `TouchForeground(visible:true)` stamps only the caller's endpoint;
  `visible:false` nulls it; a foreign user's endpoint is untouched; unknown endpoint is a no-op.
- **`internal/api` (`presence_test.go`, real net/http via httptest):** authed `POST /api/presence`
  routes to the store scoped to the session user; unauthenticated → 401; unknown endpoint → 204
  no-op.
- **`/frontend` (`use-presence.test.tsx`):** mock timers + `document.visibilityState` +
  `navigator.sendBeacon` + the transport: heartbeats only while visible, one immediate beat on
  becoming visible, a single `sendBeacon(false)` on hide, and no traffic with no subscription.

## Non-goals

- **Not** moving iOS dedup back into the service worker — R1/R2 make the server the only safe place.
- **Not** a general presence/online-status system; `last_seen_foreground_at` exists solely to gate
  the push send and is read nowhere else.
- **No** change to *which* events notify (the `ModeBlocked`/`ModeAll` gate, `push.go:31-38`), to the
  `notify.send` outbox contract (03 §7.1), or to deep-link/tap handling (`push-sw.js:97`).
- **Not** trying to guarantee zero duplicates — false positives are explicitly acceptable; the
  design optimizes them down without ever risking a miss.

## Deferred

- **Per-event-class override.** If a class of notification must *always* push regardless of
  foreground (e.g. a future high-urgency alert), add a `forceSend` flag on the `notify.send`
  payload that bypasses the `foregrounded()` check. Not needed for Blocked (the open board already
  surfaces it), so out of scope now.
- **Tighter dedup via the actual toast ack.** Instead of a time lease, the client could ack the
  specific in-app toast it rendered and the server could suppress only the exact acked event. More
  precise, much more state; the lease is the smaller mechanic that closes the reported bug.

## Research appendix

| # | Claim | Source(s) | Confidence |
|---|---|---|---|
| R1a | WebKit requires `userVisibleOnly:true` and revokes the subscription for a received push not answered with `showNotification()`. | [webkit.org/blog/12945/meet-web-push](https://webkit.org/blog/12945/meet-web-push/); [WWDC22 "Meet Web Push"](https://developer.apple.com/videos/play/wwdc2022/10098/); corroborated by `push-sw.js:61-75`. | **High** (mechanism) |
| R1b | The budget is ~3 silent pushes; omitting `event.waitUntil()` counts as silent. | [progressier.com writeup](https://dev.to/progressier/how-to-fix-ios-push-subscriptions-being-terminated-after-3-notifications-39a7); [Apple Dev Forums 727887](https://developer.apple.com/forums/thread/727887); [firebase-js-sdk#8010](https://github.com/firebase/firebase-js-sdk/issues/8010). | **Medium** — count is empirical, **not** in any official Apple/WebKit doc. |
| R2 | The penalty is for received-but-not-shown; never-sending is unobservable to WebKit and penalty-free. | Inference from R1a (webkit.org/blog/12945). | **Medium-High** — entailed, not stated verbatim anywhere. |
| R3 | `visibilitychange` is the reliable foreground signal; `pagehide`/`beforeunload`/`unload`/`freeze` are not guaranteed on mobile; iOS freezes a backgrounded PWA (~5s grace) and may fire no leave event on OS-kill/discard/crash/network-loss. `freeze`/`resume` are Chromium-only. | [Chrome Page Lifecycle](https://developer.chrome.com/docs/web-platform/page-lifecycle-api); [MDN Page Visibility](https://developer.mozilla.org/en-US/docs/Web/API/Page_Visibility_API); [igvita.com](https://www.igvita.com/2015/11/20/dont-lose-user-and-app-state-use-page-visibility/); [firt.dev iOS PWA notes](https://medium.com/@firt/whats-new-on-ios-12-2-for-progressive-web-apps-75c348f8e945). | **High** (direction); Medium on exact iOS timings (secondary, version-dependent). |
| R4 | An open SSE/WS connection ≠ foreground; a frozen PWA can hold a connection while hidden. | Chrome Page Lifecycle (above) + deployment experience. | **High** |
| R5 | RFC 8030 offers no device-liveness query; a 2xx means "queued," not "delivered"; servers learn death lazily via 404/410 (treat both as delete). | [RFC 8030](https://www.rfc-editor.org/rfc/rfc8030); [pushpad web-push errors](https://pushpad.xyz/blog/web-push-errors-explained-with-http-status-codes). Matches `sender.go:88`. | **High** |
| R6 | Discord suppresses mobile push while a user is active, gated on a configurable idle/AFK timeout (server-side, presence-driven). Slack/WhatsApp/Linear unverified. | [Discord support: inactive timeout](https://support.discord.com/hc/en-us/community/posts/360039981112); [DiscordNotificationDebug](https://github.com/xaviergmail/DiscordNotificationDebug). | **Medium-High** (Discord); **Low/unverified** (others). |
| R7 | Presence heartbeat + TTL-staleness is the standard pattern; ~30s heartbeat / 60–90s window, immediate beat on `visibilitychange`; `navigator.sendBeacon` is the best-effort leave signal. | [oneuptime presence](https://oneuptime.com/blog/post/2026-02-02-websocket-presence-detection/view); [MDN sendBeacon](https://developer.mozilla.org/en-US/docs/Web/API/Navigator/sendBeacon). | **High** (pattern); Medium (exact numbers vary). |
