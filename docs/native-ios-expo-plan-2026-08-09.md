# Kiln on iPhone — a real installable app via Expo, with cloud-shipped frontend updates

**Date:** 2026-08-09
**Status:** **PLAN ONLY — nothing implemented.** No Expo scaffold, no dependency, no code change
has been made for this. This document is the reviewable artifact; implementation tickets come
after it is accepted.
**Scope:** the mobile client only. The backend (`/backend`), the wire schema (`/schema`), and the
desktop web experience (spec `13`) are affected at their edges and are called out where they are,
but nothing here proposes rewriting them.
**Specs it touches:** `02` §10 (notifications), §11 (frontend); `07`/`08` (the client and the
primary screen); `09` (voice); `10` (infrastructure); `12` (multi-project); `13` (desktop).
**Facts to re-verify at implementation time:** everything in §9 marked ⚠︎. Expo SDK details, EAS
pricing, and App Review guideline numbering all move faster than this document will.

---

## 0. The one-paragraph version

Kiln's mobile client can become a real, installable iPhone app, and the cloud-update property the
user wants is the normal way Expo apps ship — EAS Update pushes a new JS bundle to installed apps
without App Review, and only *native* changes (permissions, native modules, icon, entitlements,
SDK bumps) need a submission. The cost is not evenly spread. Roughly **8,200 lines of this
client's logic are already DOM-free and port essentially as-is** — the L1–L3 layers that
`docs/shell-architecture-plan-2026-08-08.md` extracted, the stores, the transport module, the
voice commit machine, the generated wire types. Roughly **4,900 lines of mobile view code and
3,900 lines of mobile CSS do not port at all** and must be re-expressed in React Native. Three
things are genuinely hard rather than merely laborious: **the voice mic path** (`09` has no React
Native equivalent of `AudioWorklet` and this is the one item that could change the plan),
**notifications** (Web Push/VAPID is not APNs, so `backend/internal/push` gains a second sender),
and **frontend/backend version skew**, which is impossible today because the Dockerfile embeds the
SPA into the Go binary and becomes possible the moment the frontend ships on its own channel. The
recommendation is **Path C**: a real Expo app whose *primary screen* is native React Native reusing
L1–L3 verbatim, with the peripheral screens (dashboard, projects, onboarding, sign-up) served as
web inside the app until they earn a rewrite — and the desktop/kanban web app left entirely alone.

---

## 1. What was actually asked for

Three requirements, restated so §7 can check the plan against them:

| # | Requirement | Met by |
|---|---|---|
| R1 | A real installable iPhone app — not "add to home screen" | EAS Build → App Store / TestFlight (§6 S2) |
| R2 | Frontend changes go live immediately after deploy, like web does today | EAS Update channel (§5, §6 S3) |
| R3 | App Review is not a bottleneck for day-to-day changes | The review boundary in §5 — and Path C is chosen partly to keep native surface small |

R3 is the requirement that shapes the architecture, not R1. Any of the three paths in §3 gives you
R1. The one that best serves R3 is the one that puts the fewest things on the native side of the
line in §5, and then *changes them rarely*.

---

## 2. What exists today

### 2a. The client is a Vite + React 18 DOM SPA, embedded in the Go binary

`frontend/package.json` — React 18.3, `react-router-dom` 7, `vaul` (the iOS-style drawer),
`react-markdown` + `remark-gfm`, `@sentry/react`, three `@fontsource` variable families. Vite 5
builds it (`frontend/vite.config.ts`), and `backend/Dockerfile:46-48` copies `frontend/dist` over
`internal/web/dist` so `//go:embed` bakes the SPA **into the same binary as the backend**. One
Render service, one Docker build, one deploy (`render.yaml`, `autoDeploy: true` on `main`).

**That single-artifact property is load-bearing for §8's skew section.** Today a frontend and the
backend it talks to cannot disagree about the wire contract, because they are literally the same
file.

### 2b. It is already a PWA, and that is exactly what the user says is not enough

`frontend/index.html:64-70` links a static `public/manifest.webmanifest` with
`"display": "standalone"`; `src/standalone.ts` detects a home-screen launch via
`navigator.standalone`; `src/components/DefaultRoute.tsx` uses that to redirect an installed launch
straight to `/app`; `docs/onboarding.md:158` tells the user to add it to the home screen. Web Push
is wired end to end (`src/stores/use-web-push.ts` → `public/push-sw.js` →
`backend/internal/push/sender.go`, VAPID keys in `render.yaml`).

So the "add to home screen" experience is not a gap to be filled — it is the thing being replaced.
Everything in §4's *rework* column is work that exists only because the target is a real binary.

### 2c. The client already has the layering that makes a native shell tractable

This is the single most important pre-existing asset, and it is not luck — it landed on
2026-08-08 (`docs/shell-architecture-plan-2026-08-08.md`, DELIVERED). The architecture is four
layers, of which **only the top is per-shell**:

```
L1  intents        components/ticket-intents.ts     useTicketActions()      what we may DO
L2  reading model  components/feed-model.ts         readFeed() → FeedRow[]  what there IS to show
L3  behaviour      components/use-ticket-overlay.ts + TicketDetailHost.tsx  what we REMEMBER
L4  shells         PrimaryScreenView / DesktopScreenView / KanbanScreenView what it LOOKS like
```

with a documented rule — *"a shell file contains no `function` declaration and no `useState` above
its component body"* — enforced by an eslint `no-restricted-syntax` rule and a shared conformance
suite (`components/feed-shell-conformance.test.tsx`).

I verified the layers are actually DOM-free rather than nominally so. Count of references to
`document.` / `window.` / `navigator.` / `localStorage` / `matchMedia` / `EventSource` /
`addEventListener`:

| Module | DOM references |
|---|---|
| `components/feed-model.ts` | **0** |
| `components/ticket-intents.ts` | **0** |
| `components/use-ticket-overlay.ts` | **0** |
| `components/TicketDetailHost.tsx` | **0** |
| `components/feed-kinds.ts`, `feed-format.ts` | **0** |
| `stores/session.tsx` | **0** |
| `voice/commit-machine.ts` | **0** |

**A React Native mobile screen is, structurally, a fourth shell.** That is the claim this whole
plan rests on, and §4 is the audit that tests it layer by layer.

### 2d. Where the DOM coupling actually is

The couplings outside L4 are few, named, and each has a known React Native counterpart. This is
the complete list — I grepped for it rather than estimating:

| File | Coupling | React Native answer |
|---|---|---|
| `transport/transport.ts:291-343` | `new EventSource(appPath('/stream'))` + four `addEventListener`s | `react-native-sse`, or `expo/fetch` streaming ⚠︎ (§9 Q3) |
| `transport/transport.ts:738-755` | `navigator.sendBeacon('/api/presence')` for the leave beacon | A plain `fetch` on `AppState` change — RN has no unload race to beat |
| `stores/use-presence.ts:63-125` | `document.visibilityState` + `visibilitychange` + `pagehide` | `AppState` (`active`/`background`) — a cleaner signal than the web's |
| `stores/use-presence.ts:51` | `navigator.serviceWorker.getRegistration` | Not needed; `expo-notifications` owns this |
| `stores/current-project.tsx:40-73` | `window.location.search`, `window.localStorage` | Deep-link params via `expo-linking`; `AsyncStorage`/`expo-secure-store` |
| `stores/deep-link.ts:56-71` | service-worker `message` for notification taps | `expo-notifications` response listener |
| `theme.ts:18-19` | `documentElement.dataset.theme`, `<meta name="theme-color">` | `useColorScheme()` + native status-bar API |

Seven items. Every one is a swap at a seam that already exists, not a redesign.

### 2e. Size of the thing

| Bucket | Lines | Ports? |
|---|---|---|
| Logic: L1–L3, stores, transport, voice machine, generated wire types, theme | **8,210** | Yes — see §4 for the seven exceptions above |
| — of which `schema/generated.ts` (generated, never hand-edited) | 2,294 | Yes, unchanged — same generator, same contract |
| Mobile view code (L4 + mobile-only components) | **~4,890** | **No** — React Native has no `<div>` |
| Mobile CSS (`PrimaryScreen.css` 2,842 + `TicketDetail.css` 1,042) | **3,884** | **No** — but see §4d, the token sheet does |
| `styles/tokens.css` | 314 | As a JS token module — mechanical |
| Web-only peripheral screens (dashboard 1,676 CSS + guide + landing + signup + projects) | ~3,800 CSS + views | **Not in scope for rewrite** — see Path C |
| Tests | 24,362 | Logic tests yes; DOM-rendering tests no (§4e) |

---

## 3. Three shapes this could take

### Path A — WebView wrapper

An Expo app that is a full-screen `react-native-webview` pointed at `https://trykiln.dev/app`.

*For:* days of work, not months. Every frontend change is already instant — it is a web deploy.
Zero rework of the 8,780 lines of view code and CSS.

*Against, and this is disqualifying:* **App Store Review Guideline 4.2 (Minimum Functionality)**
explicitly targets apps that are "simply a repackaged website" ⚠︎. Kiln-in-a-WebView with no native
capability is the canonical rejection case. It can sometimes be rescued by adding real native
integrations (push, share sheet, widgets) — but at that point you are building Path C anyway, with
the WebView still there as a liability. Secondary problems: `getUserMedia` inside `WKWebView`
works but needs explicit permission plumbing and is a persistent source of iOS-version-specific
breakage for exactly the feature (`09` voice) that is Kiln's differentiator; and the app dies
completely when the network does, with no native shell to say so.

**Rejected as the destination.** It is, however, a legitimate *component* of Path C — see §3c.

### Path B — Full React Native rewrite

Rewrite every screen — mobile primary, dashboard, projects, onboarding, sign-up, landing — in
React Native. Retire the mobile web client.

*For:* one client, no WebView, best possible native feel throughout.

*Against:* it rewrites ~8,700 lines of view code and ~7,700 lines of CSS, most of it for screens a
user visits **once** (onboarding, sign-up) or **rarely** (dashboard config forms). It also forces
an immediate answer to the voice question (§4c) and the desktop question (spec `13` — the kanban
and desktop shells are explicitly *desk* readings and have no place in an iPhone app, so they'd
have to stay web regardless, meaning Path B does not actually eliminate the web client).

**Rejected as scoped.** Path B is the ten-year destination, not the migration.

### Path C — Native primary screen, web periphery *(recommended)*

A real Expo/React Native app where:

- **The primary screen (`08`) is native.** Feed + dock + ticket detail + voice, written as a fourth
  L4 shell over the existing L1–L3. This is the screen the user is in every day, it is the one that
  needs 60fps gestures and real push, and it is the one that justifies the app's existence under
  guideline 4.2.
- **Peripheral screens are the existing web app, in an in-app browser.** Dashboard, projects,
  onboarding, sign-up open via `expo-web-browser` (`ASWebAuthenticationSession`/`SFSafariViewController`)
  against the live site. They keep working, keep shipping on the web's cadence, and cost zero
  rewrite. They are configuration surfaces, visited rarely, and they are *already* mounted outside
  the app's `SessionGate` (`main.tsx` — `/dashboard`, `/projects`, `/signup` each own their
  provider), so this seam already exists in the routing.
- **The desktop/kanban experience stays web, untouched.** Spec `13` D8 already decided the two
  layouts do not share a DOM shape. Nothing changes there.

*For:* satisfies 4.2 with real native substance; confines the RN rewrite to ~4,890 lines of view
code and ~3,884 lines of CSS in **one** screen family; leaves the web client fully alive so nothing
is lost during migration; and it is reversible at every stage.

*Against:* two clients over one wire contract, permanently. The `feed-shell-conformance.test.tsx`
suite has to grow a React Native adapter or the two shells will drift — which is precisely the
failure the shell-architecture plan was written to prevent, now with a platform boundary in the
middle of it. That is a real cost and §8 does not hide it.

### 3c. Why the WebView still appears in the recommended path

Path C uses the browser for peripheral screens. That is not Path A in disguise: the *app's primary
purpose* — the feed, the dock, voice, notifications — is native, and 4.2 is a judgment about the
app, not about whether any web content appears in it. Mixed apps of this shape are routine ⚠︎. The
line to hold is: **anything a user touches daily is native; anything they touch monthly can be
web.** If that line moves — if the dashboard becomes a daily surface — it moves into the native
side, not the other way.

---

## 4. Portability audit, layer by layer

### 4a. Carries over essentially unchanged

| What | Evidence | Note |
|---|---|---|
| `schema/generated.ts` (2,294 lines) | Generated from `/schema` | Same generator, same contract, both clients. The `wire-schema` regen rule is unchanged. |
| L1 `ticket-intents.ts` (194) | 0 DOM refs | The seven ticket callbacks and their failure recovery. Verbatim. |
| L2 `feed-model.ts` (241) | 0 DOM refs | `readFeed()` decides membership/order/divider/seen/dismissability once. This is why the native shell can be a `return (…)`. |
| L3 `use-ticket-overlay.ts` (81), `TicketDetailHost.tsx` (120) | 0 DOM refs | `TicketDetailHost` renders `TicketDetail`, which is *not* portable — so this one is portable in structure, and its child needs a native sibling. |
| `feed-kinds.ts`, `feed-format.ts`, `ticket-dependencies.ts` | 0 DOM refs | The card taxonomy, one source of truth (`refactor/feed-card-kind-taxonomy`). |
| Stores: `board-store`, `feed-store`, `activity-store`, `session`, `project-status`, `project-cache` | ≤4 DOM refs each | React context + hooks are platform-neutral. |
| `voice/commit-machine.ts`, `pcm-batch.ts`, `volume-meter.ts` | 0 DOM refs | The commit state machine and the PCM framing math survive any transport. |
| `transport/transport.ts` (1,071) minus the SSE block and the beacon | §2d | ~950 of 1,071 lines are `fetch` + type guards. Both survive. |
| The design tokens (`styles/tokens.css`, 314) | — | As *values*. See 4d. |

### 4b. Needs a native counterpart written (mechanical, but real)

Everything in L4 and the mobile-only components under it:

| Component | Lines | React Native shape |
|---|---|---|
| `TicketDetail.tsx` | 1,137 | The big one. Uses `vaul`'s `<Drawer>` (`:69`) — replace with `@gorhom/bottom-sheet` or a native modal ⚠︎ |
| `Dock.tsx` | 578 | Keyboard-avoiding composer + mic. Pairs with `use-keyboard-viewport.ts` (139), which **disappears** — RN's `KeyboardAvoidingView` is the platform answer to the exact iOS visual-viewport problem that file exists to work around |
| `PrimaryScreenView.tsx` | 420 | The shell. Per the L4 rule it is markup only — the most mechanical file in this table |
| `FeedCardItem.tsx` | 428 | Uses `react-markdown` — needs `react-native-markdown-display` or similar ⚠︎ |
| `ActivityRow.tsx` | 324 | |
| `TicketDetailSandboxMenu` / `Transcript` / `VoiceActions` | 562 | |
| `HeaderStatusMenu` (194), `NotificationSettingsMenu` (169), `ProjectSwitcher` (112), `MicButton` (113), `SystemAlertBand` (46), `TicketCard` (25), `SessionGate` (98) | 757 | |
| `SwipeToDismiss.tsx` (143), `use-pull-to-refresh.ts` (156) | 299 | **Gets simpler.** `react-native-gesture-handler` + `RefreshControl` replace hand-rolled touch math |
| `PrimaryScreen.tsx` (229) | | Container — mostly wiring, mostly portable, but its router usage changes (§4f) |

Two of these get *better* in the move (`use-keyboard-viewport`, `use-pull-to-refresh`) because they
exist to reimplement things iOS gives you natively. That is worth saying out loud: not every line
in this table is a loss.

### 4c. The hard one — voice (`09`)

This is the item that could change the plan, and it deserves more than a table row.

Today (`voice/assemblyai-client.ts`): `getUserMedia` → `AudioContext` →
`audioWorklet.addModule(pcm-worklet)` → the worklet decimates the device's 44.1/48 kHz Float32
frames to 16 kHz mono PCM16 off the main thread → binary WebSocket frames straight to
`wss://streaming.assemblyai.com/v3/ws` with a backend-minted temp token. Audio never transits the
Kiln backend (`09` §2).

**React Native has no `AudioWorklet` and no `AudioContext`.** The three options:

1. **A community real-time PCM library** (e.g. an `expo-audio-stream`-class package that surfaces
   live PCM buffers) ⚠︎. Cheapest if one is healthy at implementation time. Risk: this class of
   library has a poor maintenance track record, and it becomes a *native* dependency — every
   upgrade is an App Store submission (§5).
2. **A custom Expo config plugin / native module** wrapping `AVAudioEngine` to emit 16 kHz PCM16.
   Most control, best fit to `09`'s existing frame contract (`pcm-batch.ts` already owns the
   decimate-and-batch math and would be reused as-is), no third-party maintenance risk. Cost: real
   Swift, and it is native — so it is on the review side of the line forever.
3. **Keep voice in a hidden WebView.** `WKWebView` supports `getUserMedia` from iOS 14.3 with
   `NSMicrophoneUsageDescription` and the right permission grant type. Preserves
   `assemblyai-client.ts` untouched. Rejected as a *destination* — it puts the differentiating
   feature on the most fragile mechanism in the app — but it is a legitimate **stage-4 fallback**
   if option 1 or 2 slips.

**Recommendation: spike option 1 and option 2 head-to-head before committing** (§6 S1). This is the
one place where the plan should not pick a winner from a document. Note that `09`'s architecture
survives all three: the token minting, the commit machine, the neutral `VoiceProviderEvent`s, and
the PCM framing are all provider- and platform-agnostic already. Only the ~120 lines of mic-and-
socket plumbing in `assemblyai-client.ts` are at stake.

Also worth flagging: iOS background-audio and audio-session category behaviour is a genuinely
different world from the browser's. `voice-store.tsx:332` already reasons about "the
play-and-record audio session going inactive" — that comment is about iOS Safari, and the native
version of that concern is `AVAudioSession` configuration, which is native config, which is
reviewed.

### 4d. Styling — 3,884 lines of mobile CSS

React Native has no CSS. The options are hand-written `StyleSheet` objects, or a
CSS-in-JS-for-RN library (NativeWind/Tailwind-for-RN, Tamagui, unistyles) ⚠︎.

The mitigating fact: `styles/tokens.css` is already **the single source of styling truth**, with a
hard rule in its header — *"Component CSS must consume ONLY the semantic aliases, never the raw
ramps and never literal colors"* — and a `stylesheet-discipline.test.ts` enforcing it. The recent
`chore/design-tokens-close-out` commit removed the last raw colours. So the *palette* transfer is a
mechanical 314-line CSS-custom-properties → TS-object conversion, and it should be done as a
**shared** module consumed by both clients, not a copy. Everything downstream of that is layout,
which is where the real work is.

**Do not attempt to port the CSS.** Re-express the screens against the token module. A translated
stylesheet is the worst of both — it will neither look right nor read like React Native.

### 4e. Tests — 24,362 lines, and roughly half of them come along

Logic tests (`feed-model.test.ts`, `ticket-intents.test.tsx`, `commit-machine.test.ts`,
`transport.test.ts`, the store tests) run under vitest against platform-neutral code and are
unaffected. Tests that render DOM and query it — the `__snapshots__` directories,
`PrimaryScreenView.test.tsx`, `Dock.snapshot.test.tsx` — do not transfer; the native shell needs
its own rendering tests (`@testing-library/react-native` ⚠︎).

**The important one is `feed-shell-conformance.test.tsx`.** It is a `describe.each` over shell
adapters (`{ render, rowSelector, dividerSelector, showEarlierSelector }`) asserting shared
semantics once against every shell's DOM. Adding a React Native adapter to it is the mechanism that
stops the native and web shells drifting, and per §3's "against" column it is the load-bearing
mitigation for Path C's one real architectural cost. **It should be a named deliverable of the
stage that lands the native shell, not a follow-up.**

The `make check` gate (`AGENTS.md`, the `end-to-end-development` skill) must grow the native
project's lint/typecheck/test, or the new client sits outside the wall that everything else is
behind.

### 4f. Routing, auth, and session

- **Routing.** Nine non-test files import `react-router-dom`. Of those, only `PrimaryScreen`'s
  neighbourhood matters for the native app — `ProjectSwitcher.tsx` and `DefaultRoute.tsx`. The rest
  (`Landing2`, `Guide`, `Signup`, `Settings`, `ProjectsManager`, `NotFound`) are the web-periphery
  screens Path C keeps on the web. Native routing is `expo-router` or React Navigation ⚠︎.
- **Auth is the fiddliest piece and is easy to get wrong.** Today: a GitHub App OAuth flow through
  backend routes (`auth/github-connect.ts` — `/auth/github/connect`), ending in a session cookie
  set `HttpOnly`, `SameSite=Lax`, `Secure` when TLS (`backend/internal/api/session.go:154-191`).
  The comment there is emphatic that these navigations must be **real full-page loads, never a
  router `Link`** — which is exactly the property that makes them work inside
  `expo-web-browser`'s `openAuthSessionAsync` (`ASWebAuthenticationSession`), the correct native
  vehicle for a third-party OAuth flow. Two sub-decisions fall out, and they are §9 Q1:
  - Native `fetch` on iOS uses `NSURLSession`'s shared cookie store, so a `SameSite=Lax` session
    cookie *can* survive into app requests — but relying on that is fragile and invisible when it
    breaks. The alternative is a bearer-token session for native clients, which is a **backend
    change** (`internal/api/session.go`) and touches `/schema`.
  - `KILN_PUBLIC_URL` (`render.yaml`) redirects every browser GET onto `https://trykiln.dev`. The
    native app's callback needs a registered custom scheme or Universal Link; that interacts with
    the single registered GitHub App callback URL that §`render.yaml`'s comment records as having
    already broken sign-in once.
- **Deep links.** `stores/deep-link.ts` parses `window.location.search` and listens for
  service-worker messages (notification taps); `stores/current-project.tsx:40` reads the project id
  from the query string. Native equivalent: `expo-linking` + the `expo-notifications` response
  listener. The *shape* — "a notification tap names a project, else the MRU, else the first"
  (`12` §6.3) — is unchanged.

### 4g. Notifications — this is a backend change, not just a client one

`02` §10 is implemented as **Web Push**: VAPID keys, RFC 8291 encryption, a
`SherClockHolmes/webpush-go` sender (`backend/internal/push/sender.go`), a static
`public/push-sw.js`, and subscription endpoints stored per user. **None of that reaches a native
iOS app.** Native push is APNs, via `expo-notifications` and Expo's push service (or direct APNs
with a `.p8` key).

What has to happen:

- `internal/push` grows a second sender behind the existing seam. The good news: the sender is
  already constructed at the composition root only when VAPID env is set, and the runtime otherwise
  keeps a log-only notifier — so there is a real port there to widen rather than a hardcoding to
  undo.
- The subscription payload changes shape (an Expo/APNs push token is not
  `{endpoint, keys:{p256dh, auth}}`), which means **`/schema` changes** and both sides regenerate.
- The presence-dedup logic (`sender.go`, `presenceTTL = 30s`, the foreground-lease check that
  suppresses a push when the in-app toast already covers it) is *transport-independent* and should
  be reused, not reimplemented. `AppState` gives a cleaner foreground signal than
  `visibilitychange` + `pagehide` + the beacon-that-might-not-fire, so this actually gets more
  reliable.
- APNs requires an Apple Developer account, a push key, and the `aps-environment` entitlement —
  all native, all reviewed, all one-time.

**One genuine win:** iOS Web Push only works for an *installed* PWA and is comparatively
restricted. Native APNs is unconditional. `02` §10's whole reason for existing — "reaching the user
when the app is backgrounded or closed" — is better served after this migration than before.

---

## 5. The review boundary: what ships instantly, what waits for Apple

This is the section the user's R2/R3 depend on, so it is stated as a rule with a table rather than
prose.

**The rule.** EAS Update ships a new **JavaScript bundle and its bundled assets** to installed apps
over the air. Each build declares a **runtime version**; an update is only delivered to builds whose
runtime version matches. **Anything that changes the native binary changes the runtime version, and
a changed runtime version means a new build and a new App Store submission** ⚠︎.

| Ships instantly via EAS Update (no review) | Requires EAS Build + App Store submission |
|---|---|
| React components, screens, layout, styles | Adding/removing/upgrading any **native** module (incl. the voice mic module, §4c) |
| Copy, labels, the resting-state wording, `kiln-words` | New **permission** strings (`Info.plist` usage descriptions) |
| Business logic — L1 intents, L2 reading model, L3 behaviour | New **entitlements** (push, associated domains, background modes) |
| Bug fixes in anything JS | **App icon**, launch screen, display name, bundle identifier |
| New screens built from existing native primitives | **Expo SDK / React Native version** bumps |
| Client-side wire-contract changes (regenerated types) | Changes to the **privacy manifest** / App Privacy answers |
| Images and fonts bundled through the JS asset graph | Anything altering the app's **primary purpose** (see below) |
| Feature flags, copy-level experiments | The first ever build, obviously |

**Apple's actual position on OTA, stated honestly.** Two guidelines pull in different directions
⚠︎. Guideline 2.5.2 says apps should be self-contained and may not download code that changes
features. The Developer Program License Agreement §3.3.2 carves out the exception everyone relies
on: interpreted code *may* be downloaded, provided it does not **materially change the primary
purpose of the app** as submitted and approved, does not create a store or storefront, and does not
circumvent signing or the sandbox. React Native OTA updates (CodePush, and now EAS Update) have
operated inside that carve-out for roughly a decade, at very large scale. It is well-trodden, not
a grey-market trick.

**But the constraint is real and it is a product constraint, not a technical one:** you can ship
anything you like over the air *as long as Kiln remains the app Apple approved*. Fixing the feed,
restyling the dock, changing what a card says, adding a ticket action — all fine. Turning Kiln into
a different product without resubmitting is the thing that is not fine, and the practical risk is
not a rejection (there is no review to reject) but a **removal** if it is noticed. For a
single-purpose orchestrator app this is a constraint you will never bump into by accident.

**Timing nuance the user should know about — "instantly" means something slightly different.**
Today a web deploy is live on the user's next page refresh. An EAS Update is fetched and applied on
the app's **next launch** by default; a mid-session update requires explicitly calling
`expo-updates`' fetch-and-reload API ⚠︎. So the honest phrasing of R2 after this migration is
*"live without App Review, applied on next app open"* — with an opt-in path to sooner. That is a
small regression against the web's behaviour and it should be a conscious choice (§9 Q4), because
"reload the app under the user mid-conversation" is a hostile thing to do to someone dictating a
ticket by voice.

**Also note App Review is much less of a wall than its reputation** ⚠︎: Apple reports the large
majority of submissions reviewed within 24 hours, and **TestFlight** internal distribution needs no
full review at all — which matters enormously here, because Kiln is currently a single-user
pre-production app (`AGENTS.md`: *"This app is not used by anyone"*). For the foreseeable future
TestFlight *is* the distribution channel, and the App Store is a later step.

---

## 6. The migration, in stages

Each stage is independently valuable and independently abandonable. Nothing after S0 is committed
by starting S0.

### S0 — Decisions and prerequisites *(no code)*

Answer §9's open questions. Enrol in the Apple Developer Program (⚠︎ ~$99/yr), create the App ID,
APNs key, and bundle identifier. Confirm EAS plan and pricing ⚠︎. **Exit:** §9 has no unanswered
Q1–Q4.

### S1 — Two spikes, in parallel, timeboxed *(throwaway code, in a scratch worktree)*

- **Spike A (the one that matters): voice.** Options 1 and 2 from §4c, head to head, on a real
  device. Success criterion: 16 kHz mono PCM16 frames reaching AssemblyAI's socket with
  `pcm-batch.ts` reused unmodified, and a clean stop/teardown. **If both fail, the plan changes** —
  §4c option 3 becomes the design and that is a different document.
- **Spike B: auth.** GitHub App flow through `expo-web-browser`, ending with an authenticated
  `GET /api/me`. Determines §9 Q1 (cookie vs. bearer) with evidence.

**Exit:** a written recommendation on each. This is the only stage that can invalidate the plan, so
it comes before anything is built.

### S2 — The Expo app exists and installs

A minimal Expo project in the repo (`/mobile`, alongside `/frontend` — see §9 Q2 for the monorepo
question). One screen. EAS Build producing an installable `.ipa`; EAS Submit to TestFlight. Icon,
splash, bundle id, permissions declared **now** rather than later, because every one of them is on
the review side of §5's line and you want them settled before the cadence matters.

**Exit:** the app is on the user's phone, from TestFlight, and it opens. R1 is met — with a
placeholder inside it.

### S3 — The update pipeline, proven before it is needed

Wire EAS Update: channels (`production`, `preview`), runtime-version policy, and the CI step that
publishes an update on merge to `main`. **Then deliberately test the boundary**: ship a JS-only
change and confirm it lands without review; attempt a native change and confirm it correctly
demands a new build.

**Exit:** R2 and R3 are demonstrated, not assumed, on a trivial app. Doing this before the real
screen exists means the pipeline is never the unknown while the UI is also the unknown.

### S4 — The shared core

Extract the platform-neutral layer so both clients consume one copy rather than two: L1–L3, the
stores, the schema types, the token module, the voice commit machine, and a transport whose SSE and
presence pieces sit behind small platform-swappable seams (§2d). This is the stage that decides
whether Path C's "two clients, one contract" is a strength or the drift problem in §3.

**Exit:** `/frontend` builds and passes `make check` consuming the shared core, with **no behaviour
change on the web**. That is the safety property — if the web app is unchanged, the extraction was
faithful.

### S5 — The native primary screen

Build the fourth L4 shell: feed, dock, ticket detail, voice, notifications. In dependency order:
transport + session + a read-only feed → intents and writes → ticket detail → gestures
(swipe-to-dismiss, pull-to-refresh) → voice → APNs (with the backend sender from §4g). Peripheral
screens open in `expo-web-browser`.

**Named deliverable, not a follow-up:** the React Native adapter for
`feed-shell-conformance.test.tsx` (§4e).

**Exit:** the native app does everything the mobile web app does, and the conformance suite asserts
both shells agree.

### S6 — Cut over

Point the user at the native app. The mobile web client **stays alive and shipping** — it is the
fallback, it is the desktop entry point, and per spec `13` the desktop/kanban shells were never
going native anyway. Decide then, with evidence, whether the mobile web shell is retired or kept
(§9 Q5). App Store submission proper, if and when Kiln is meant for people beyond its author.

---

## 7. Does this actually meet R1–R3?

| | Met? | With what caveat |
|---|---|---|
| **R1** — a real installable iPhone app | **Yes**, at S2 | Via TestFlight first; App Store only when the product is meant for others |
| **R2** — frontend changes live immediately | **Yes**, at S3 | With a real nuance: applied on **next app launch** by default, not on the spot (§5). Mid-session reload is available and is a deliberate product choice, not a default |
| **R3** — App Review not a day-to-day bottleneck | **Yes** | Provided the native surface stays small and stable. §5's right-hand column is the list to keep short — and it is the reason Path C beats Path B, which would put the entire UI's native dependencies on that side of the line |

---

## 8. Tradeoffs, risks, and the thing nobody asks about

**R1 — Voice is the schedule risk.** §4c. Mitigated by putting it in S1 rather than discovering it
in S5. If both native options fail, the WebView fallback keeps the feature but on a worse
foundation.

**R2 — Two shells will drift.** The exact failure `docs/shell-architecture-plan-2026-08-08.md`
exists to prevent, now across a platform boundary where a shared conformance test is harder to
write and easier to skip. Mitigated by making the RN conformance adapter a deliverable of S5. If
that slips, this risk is realised — treat a slip there as a schedule problem, not a nice-to-have.

**R3 — Frontend/backend version skew becomes possible for the first time.** *This is the risk
nobody asks about, and it is the one most specific to this repo.* Today `backend/Dockerfile:46-48`
embeds the SPA into the Go binary: frontend and backend ship as one artifact and **cannot**
disagree about the wire contract. The moment the frontend ships on its own EAS channel, an app on
an old JS bundle can talk to a new backend — and `AGENTS.md` explicitly says *"No need to add
feature flags or support backward compatibility at this stage"*, which is a policy written for a
world where skew was impossible. It stops being true here. Worse, a user who has not opened the app
in three weeks is running three-week-old client code against today's backend.

*This does not need solving in this plan, but it needs deciding before S6:* either the wire
contract gains a compatibility discipline (additive-only changes, or a version handshake with a
"please update" screen), or `/schema` changes become a native-coordination event. The second is
much worse for R3. **Recommendation: additive-only wire changes plus a minimum-client-version
check the backend can serve** — and that recommendation should become its own ticket, because it
changes a rule in `AGENTS.md`.

**R4 — Operational surface grows.** A second CI pipeline, EAS credentials, Apple certificates and
provisioning profiles, an Apple Developer account that expires annually. Per `10` §8, agents
operate this system; Apple's console is the one surface in the stack with no good non-interactive
API and no MCP server, so certificate rotation is a human-in-the-loop event in an otherwise
agent-operated deployment. Worth naming now.

**R5 — Cost.** Apple Developer Program ~$99/yr ⚠︎. EAS has a free tier with limited build minutes;
a paid plan is likely once builds are routine ⚠︎ — verify current pricing at S0. Against today's
~$13/mo (`10` D1) this is a real proportional increase for a single-user app.

**R6 — Sentry.** `@sentry/react` and the Vite source-map upload plugin (`vite.config.ts`) are
web-specific. `@sentry/react-native` exists and has its own symbolication story, which interacts
with OTA updates: **each EAS Update needs its own source maps uploaded**, or every native crash
report from an OTA'd build is unsymbolicated. Easy to miss, easy to wire once, painful to discover
during an incident.

**R7 — The one-way door.** Bundle identifier and app name are effectively permanent once submitted;
`aps-environment` and associated-domains entitlements are cheap to add before first submission and
annoying afterwards. Declare the full permission set in S2 even if unused, per §6.

---

## 9. Open questions — answer these before S1 ends

**Q1 — Session transport for the native client: cookie or bearer?** (§4f) Spike B decides.
Bearer is more honest and more portable but is a backend change to
`backend/internal/api/session.go` and touches `/schema`. Cookie-over-`NSURLSession` is zero backend
work and one silent-breakage class away from a bad afternoon. *Leaning: bearer, on the grounds that
an invisible dependency on shared cookie storage is exactly the kind of thing that breaks on an iOS
minor release.*

**Q2 — Repo layout.** `/mobile` beside `/frontend` in this repo, or a separate one? The shared core
(S4) argues strongly for one repo — a pnpm workspace already exists
(`frontend/pnpm-workspace.yaml`). The `make check` gate and `.github/workflows/check.yml` argue the
same way: one wall, everything behind it. *Leaning: same repo, `/mobile` + a shared package.*

**Q3 — SSE in React Native.** `react-native-sse` (a dedicated library, but a dependency) versus
`expo/fetch` streaming (first-party, newer) ⚠︎. Either way `transport.ts:291-343` gains a seam.
Note the client depends on `EventSource`'s *native retry* behaviour — `transport.ts:99` says the
reconnect story is "purely a display concern" precisely because the browser handles it. Whatever
replaces it must reproduce that, or `07` §8's connection-state handling becomes the client's
problem.

**Q4 — Update application policy.** Apply on next launch (default), or fetch-and-reload
mid-session? §5. *Strong leaning: next launch.* Reloading the JS bundle under someone who is
mid-voice-turn is worse than a slightly stale build. Possible middle ground: apply on next launch,
but reload eagerly when the app has been backgrounded for a while.

**Q5 — Does the mobile web shell survive?** Deferred to S6 deliberately, with evidence rather than
now with none. It is free to keep during the migration and it is the fallback if anything in S5
goes wrong.

**Q6 — Voice on iOS: which audio path?** §4c, decided by Spike A. The only question here that can
change the plan's shape rather than its details.

---

## 10. Recommendation

**Adopt Path C.** Ship a real Expo iPhone app whose primary screen is native React Native over the
existing L1–L3 layers, with the peripheral configuration screens served as web inside the app, and
the desktop/kanban web experience untouched.

**Do S0 and S1 first and treat them as a gate.** The voice spike is the one result that can
invalidate this document, and it costs days to find out rather than months.

**Do S3 — the update pipeline — before S5, the real UI.** Proving R2 and R3 on a trivial app means
that when the real screen is the unknown, the pipeline is not.

**Raise the version-skew question (R3, §8) as its own ticket.** It is the finding in here least
likely to be noticed by anyone else and the most likely to bite, because it quietly retires an
assumption that `AGENTS.md` currently states as policy.

What this document does **not** do is start any of it. Per the ticket, no Expo scaffold exists, no
dependency was added, and no application code was touched.
