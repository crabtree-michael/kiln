# Kiln as a real installable app via Expo — iPhone, iPad, and the desk — with cloud-shipped frontend updates

**Date:** 2026-08-09
**Revised:** 2026-08-09 — **scope widened from iPhone-only to iPhone + iPad + desktop.** The
original document planned the mobile client and left the desktop web experience explicitly
untouched. That does not survive iPad: an iOS app runs on iPad, and at iPad width Kiln's client
already renders the *desktop* shell (`13`), not the phone one. The revision adds §2f (what the
desk is, and why iPad drags it in), **Path D** in §3, §4h (the desk in React Native), the delivery
tiers and OTA consequences in §5b, the iPad decision point in S2 and the new S7 in §6, risks R8–R9
in §8, and questions Q7–Q9 in §9. Everything else is the original document and its conclusions
still stand — **Path C is not overturned; it is given a destination.** Revision marks are ⊕.
**Status:** **PLAN ONLY — nothing implemented.** No Expo scaffold, no dependency, no code change
has been made for this, before or after the revision. This document is the reviewable artifact;
implementation tickets come after it is accepted.
**Scope:** the client, on every surface it runs on — phone, tablet, and desk. The backend
(`/backend`) and the wire schema (`/schema`) are affected at their edges and are called out where
they are, but nothing here proposes rewriting them. ⊕ The desktop web experience (spec `13`) is
**no longer out of scope**: §2f explains why, and §7 records the amendment this asks of `13` D8.
**Specs it touches:** `02` §10 (notifications), §11 (frontend); `07`/`08` (the client and the
primary screen); `09` (voice); `10` (infrastructure); `12` (multi-project); `13` (desktop —
⊕ now substantively, not at the edges).
**Facts to re-verify at implementation time:** everything in §9 marked ⚠︎. Expo SDK details, EAS
pricing, React Native for Web's maturity, and App Review guideline numbering all move faster than
this document will.
**Measurement basis:** the original figures in §2e and §4 were measured at `93ad871`. ⊕ The
desktop figures added in §2f and §4h were measured at `9c6849f`, twelve commits later, where the
mobile CSS has drifted about 3% upward (`PrimaryScreen.css` 2,842 → 2,911, `TicketDetail.css`
1,042 → 1,084). The originals are left as measured rather than restated, so this document's two
passes stay separable.

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

⊕ **The revision, in one paragraph.** Path C's last clause — *the desktop web app left entirely
alone* — assumed the native app is an iPhone app. There is no such thing as an iPhone-only
*reach*: an iOS app runs on iPad, and Kiln's own breakpoint (`DESKTOP_MIN_WIDTH = 1024`) puts
every current iPad in landscape on the **desktop** shell. So the moment the app supports iPad, the
desk shell has to exist inside the binary, and "desktop stays web" stops being a scoping choice
and becomes a device-family choice nobody has made yet. §3's **Path D** is the answer for when it
is made: extend the same RN app to the desk shell, and serve the desktop *browser* from that one
source via **React Native for Web**, retiring the DOM app shell rather than maintaining a second
one. The measured cost on top of Path C is a further **~1,600 lines of desktop view code, ~465
lines of desktop logic, and ~1,580 lines of desktop CSS** — smaller than the mobile leg, and
sitting on the same L1–L3 layers. The instant-update requirement **survives intact**, because the
two desktop tiers worth having (the browser, and iPad) are the two that keep it: only a *packaged*
Mac application would reintroduce a review gate, and §5b declines it for exactly that reason. The
recommendation is unchanged in substance and sharpened in sequence: **do Path C, declare the first
submission iPhone-only because that is the reversible direction, and gate Path D on a desk-in-RN
spike (Spike C) the same way the whole plan is gated on the voice spike.**

---

## 1. What was actually asked for

Three requirements, restated so §7 can check the plan against them:

| # | Requirement | Met by |
|---|---|---|
| R1 | A real installable iPhone app — not "add to home screen" | EAS Build → App Store / TestFlight (§6 S2) |
| R2 | Frontend changes go live immediately after deploy, like web does today | EAS Update channel (§5, §6 S3) |
| R3 | App Review is not a bottleneck for day-to-day changes | The review boundary in §5 — and Path C is chosen partly to keep native surface small |
| ⊕ R4 | The plan is not iPhone-only — iPad is accounted for, and on iPad Kiln is the *desktop* experience | The device-family decision in §6 S2, and **Path D** (§3) |
| ⊕ R5 | Desktop is covered under the same native approach, without losing R2/R3 there | React Native for Web for the browser tier; the tier table in §5b |

R3 is the requirement that shapes the architecture, not R1. Any of the three paths in §3 gives you
R1. The one that best serves R3 is the one that puts the fewest things on the native side of the
line in §5, and then *changes them rarely*.

⊕ **R4 and R5 were added by the revision, and R4 is the one that changes the document.** It is not
a feature request — it is the correction of an assumption. The original plan never states a device
family, and by not stating one it quietly assumed the app's reach stops at the phone. It does not.
R5 follows from R4 rather than standing on its own: once the desk shell must exist in React Native
for iPad's sake, maintaining a *second* desk shell in the DOM for browsers is the drift risk of §8
R2 doubled, and RN-for-Web is the way to not do that.

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

### ⊕ 2f. What the desk is today — and why iPad drags it into a mobile plan

**The fact the original document missed.** There is no iPhone-only *reach*. An iOS app runs on
iPad — either as a Universal app that lays out for the larger screen, or as an iPhone app in
compatibility mode, letterboxed and scaled on a 13-inch display. What there is no version of is
"iPad users simply do not encounter this app."

And Kiln at iPad width is **not** the phone screen. The shell switch is a viewport query:

```
components/PrimaryScreen.tsx:81        const isDesktop = useIsDesktop();
components/desktop/use-desktop-layout.ts:26   export const DESKTOP_MIN_WIDTH = 1024;
                                       :30   `(min-width: ${DESKTOP_MIN_WIDTH}px)`
```

Every current iPad in **landscape** is ≥1024pt wide and therefore already gets `DesktopScreenView`
in the browser today. In **portrait** the answer depends on which iPad: mini, Air and 11-inch Pro
are 744–834pt and get the phone shell, while the **13-inch iPad Pro is exactly 1024pt and matches
`min-width: 1024px`**, so it gets the desk shell in both orientations ⚠︎.

That is a defensible accident of a responsive rule and a much less defensible thing to ship
deliberately inside an installed app — where rotating a device would change which product you are
using, on some iPads but not others. §9 Q8 asks for a call on it, and the inconsistency is the
reason it should not be settled in an implementation ticket.

So the consequence for this plan is structural, not cosmetic:

> **If the app supports iPad, the desktop shell must exist in React Native — because of iPad, not
> because of macOS.** A Mac application is a separate question (§5b), and this document still
> declines it. The desk shell's presence in the binary is not optional in the same way.

**What that shell actually is,** measured at `9c6849f`:

| Bucket | Lines | Ports? |
|---|---|---|
| Desktop view code — `DesktopScreenView` 454, `KanbanScreenView` 268, `DesktopComposer` 259, `WorkingNow` 229, `ProjectsRail` 164, `Backlog` 130, `DesktopRail` 100 | **1,604** | **No** — same reason as §2e's mobile view code |
| Desktop logic — `working-now.ts` 135, `backlog.ts` 120, `kanban-board.ts` 110, `use-desktop-layout.ts` 100 | **465** | **Yes, except `use-desktop-layout`** — the other three are 0-DOM model modules of exactly the L2 kind, and `use-desktop-layout` is one `matchMedia` call that becomes `useWindowDimensions()` |
| Desktop CSS — `DesktopScreen.css` 1,353, `Kanban.css` 227 | **1,580** | **No** — §4d applies unchanged |

**This is smaller than the mobile leg** — 1,604 view lines against §2e's ~4,890, and 1,580 CSS
lines against 3,884 — and it sits on the same L1–L3 layers, because `13` D10 put it there
deliberately. The desk is not a second application. It is a fifth L4 shell.

The other three couplings the desk adds beyond §2d's seven: `use-desktop-layout.ts`'s `matchMedia`
(above), the `:hover`-driven reveals in §4h, and roving keyboard focus — all in the shell layer,
none in the model layer.

---

## 3. ⊕ Four shapes this could take

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

> ⊕ **The parenthetical above is wrong, and its being wrong is what produced this revision.** "The
> desk shells have no place in an iPhone app" is true and irrelevant: they have every place in an
> iPad app, and an iOS app is an iPad app whether or not it was designed to be (§2f). The rest of
> Path B's rejection stands — rewriting onboarding, sign-up and the dashboard config forms in
> React Native is still work spent on screens visited once or monthly. Path D below is what Path B
> should have been: the desk included, the periphery still not.

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

  > ⊕ **Two corrections to that bullet.** First, the citation is off: `13` **D10** is the decision
  > that the shells *"differ only in DOM shape and CSS"*; **D8** is the different and more
  > consequential decision that *"desktop is the responsive web app widening out, not a separate
  > installable application"* — which is the one this revision has to amend (§7). Second, the
  > bullet holds only for an app that does not support iPad (§2f). It is therefore **retained as
  > the Path C position and superseded by Path D**, and §6 S2 is where the choice between them is
  > actually made.

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

### ⊕ Path D — Path C, plus the desk *(the destination; gated, not scheduled)*

Path C with two additions and one subtraction:

- **The desk shell becomes a fifth L4 shell in React Native**, over the same L1–L3 (§2f). The app
  becomes Universal: phone shell at phone width, desk shell at iPad width, one binary, one OTA
  channel.
- **The desktop browser is served from that same source via React Native for Web** ⚠︎ — the
  renderer Expo already uses for its web target. `trykiln.dev/app` stops being a Vite DOM build and
  becomes the RN-for-Web build of the shell that iPad runs.
- **The DOM app shell is retired.** `PrimaryScreenView`, `DesktopScreenView`, `KanbanScreenView`
  and their CSS go. The web **periphery** — landing, sign-up, dashboard, projects, onboarding,
  `/debug` — stays exactly where Path C left it, in the DOM, forever. Path D is not Path B.

*For:* it is the only shape that gives iPad the experience `13` specifies **and** does not leave
two desk shells in two languages to be kept in agreement. It also closes Path C's one admitted
architectural cost (§3's "against", §8 R2) rather than doubling it: after Path D there is one shell
source per screen family, not two. The desk leg is smaller than the mobile leg already done (§2f),
and every mechanism it needs — the L1–L3 reuse, the RN toolchain, the conformance adapter, EAS
Build, EAS Update, APNs — is already built and proven by Path C's stages. **Nothing in Path D is a
new kind of risk. It is more of the same work, on a smaller surface.**

*Against, and these are real:*

- **RN-for-Web is a third rendering target with its own fidelity gap** ⚠︎. Path C ships two shells
  (DOM web, RN native). Path D ships one shell source rendered three ways (RN iOS phone, RN iOS
  tablet, RN-for-Web browser), which is fewer *sources* but more *renderings*, and the browser
  rendering is the one this repo currently gets for free.
- **The desk's defining affordances are the ones React Native handles worst** — hover-reveal and
  keyboard navigation, which `13` §6/§9 make constitutive rather than decorative. §4h is the
  honest audit of that, and it is why Path D is gated on a spike rather than scheduled.
- **It retires a shipping, working desktop app** to replace it with a rendering of the same design
  through a different engine. The user-visible upside of that specific swap is zero; the upside is
  entirely in not maintaining two of them. That is a real reason to do it and a bad reason to
  hurry it.
- **`13` D8 has to be amended** (§7).

**Not recommended *now*, and not for the reasons Paths A and B were rejected.** Path D is
recommended as the **destination**, gated on Spike C (§6 S1) and sequenced after Path C has
shipped and proven itself. §10 states the sequencing; §6 S7 states the work.

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

### 4d. Styling — 3,884 lines of mobile CSS (⊕ and 1,580 of desktop, §2f)

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

### ⊕ 4h. The desk in React Native — what is actually at risk

The desk leg is smaller than the mobile leg (§2f) and rests on the same layers, so most of §4a–§4f
applies to it unchanged and is not repeated. What is *different* about the desk is worth stating
precisely, because it is the whole case for gating Path D on a spike.

**First, a claim to retire before it spreads.** An earlier pass of this analysis counted CSS
features across *all* of `src/**/*.css` and concluded that React Native's losses "cluster on
desktop" — 86 `:hover`, 19 `:focus-visible`, and so on. Re-measured against the files that
actually move, that is wrong: most of those counts live in `Dashboard.css` (25 hovers), `Landing2`,
`Guide`, `Signup` and `ProjectsManager` — the **web periphery**, which every path in §3 keeps in
the DOM. The honest per-shell numbers:

| Feature | Mobile CSS (moves under Path C) | Desktop CSS (moves under Path D) |
|---|---|---|
| `:hover` | 28 | **11** |
| `cursor:` | 40 | 5 |
| `:focus-visible` | 9 | **5** |
| `display: grid` / `grid-template` | 2 / 2 | 3 / 3 |
| `position: sticky` | 2 | 1 |
| `@media` | 8 | 2 |
| `@keyframes` | 11 | 2 |
| `env(safe-area-inset-*)` | 4 | 0 |

By volume the desk is the *easier* of the two. **The volume is not the point.**

**The point is which hovers those are** — ten of the eleven are in `DesktopScreen.css`, and three
of them are `13` §6 itself rather than decoration:

```
540  [data-role='desktop-feed-row']:hover      [data-role='feed-card-age']       → reveals the age
885  [data-role='desktop-working-ticket']:hover [data-role='desktop-working-age'] → reveals the age
887  [data-role='desktop-backlog-ticket']:hover [data-role='desktop-backlog-age'] → reveals the age
     …plus row/rail/send background tints, which are affordance polish
```

That is *"a row is minimal at rest and fuller on hover… A card that is quiet until you point at it
is a card you can leave in your peripheral vision"* — the mechanism the ambient design is built
on. Mobile's 28 hovers are, by contrast, almost entirely incidental: a web app that sometimes
meets a pointer.

**But the shell is already better prepared for this than a hover count suggests, and the audit
should say so.** Every one of those three reveals is written as a *pair*:

```css
[data-role='desktop-feed-row']:hover        [data-role='feed-card-age'],
[data-role='desktop-feed-row']:focus-within [data-role='feed-card-age'] { opacity: 1; }
```

with the comment at `DesktopScreen.css:519` stating the intent outright — *"`:focus-within` gives
the keyboard pass the same reveal, so hover is never the only way to see something."* There are
seven `:focus-within` rules doing this, and the keyboard half is not theoretical: `DesktopScreenView`
already implements roving focus in JS (`:305` a single `tabIndex={0}` container, `:306` an
`onKeyDown`, `:358` rows at `tabIndex={-1}` so they stay out of the Tab order).

**So the reveal is dual-path by design, and the port inherits that.** The focus path is *already*
expressed as component logic rather than as CSS, which is the half that moves to React Native
most cleanly. That materially narrows the exposure — it is not "does `13` survive without hover",
because `13` already does not depend on hover alone. Four things still need answering, in
descending order of how much they matter:

1. **Can the focus-driven reveal be expressed in RN at all?** This is now the *primary* question,
   ahead of hover. RN has no `:focus-within`, so a row's "am I or my children focused" state
   becomes explicit component state driven by the existing roving-focus handler. Tractable and
   unglamorous — but it is the path everything else falls back to, so it has to work.
2. **Hardware-keyboard support on iPad** ⚠︎. `13` §9 commits that *"a full pass — glance, read a
   blocker, answer it — should be possible without the mouse."* Given (1), the roving-focus logic
   ports; what needs verifying is that iOS delivers the key events to it with a Magic Keyboard
   attached.
3. **Hover on iPad with a pointer** ⚠︎. `react-native-web` supports hover, so the *browser* tier
   is fine and this is only about iPad-with-a-trackpad. Given the `:focus-within` pairing, losing
   it is a **degradation, not a failure** — the ages stay reachable by keyboard and the rows lose
   a lift. Worth knowing; no longer plan-shaped.
4. **The sticky working strip** (`13` D9 — *"holds its own height and does not scroll out of
   sight"*) and **the right-edge overlay with its scrim** (D7a/D7b — a 460px full-height panel,
   `--scrim` over everything left of it). Both are layout arrangements in RN rather than CSS
   properties; both were revised more than once in `13` and so are worth asserting rather than
   assuming.

**None of these is a known blocker, and the one that looked worst on first inspection turned out
to be the best-defended.** They are unknowns, and they are cheap to resolve — which is precisely
the argument S1's Spike A makes for voice, applied to the desk. Hence Spike C.

**One thing that gets easier, for symmetry with §4b's note.** `use-desktop-layout.ts`'s `matchMedia`
subscription becomes `useWindowDimensions()`, which is both simpler and correct on rotation — and
rotation is a thing iPads do and browsers largely do not.

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

### ⊕ 5b. What widening to iPad and the desk does to R2 and R3

The requirement is unchanged and it survives. What changes is that it stops being one mechanism.

**The delivery tiers, ranked by what they cost and what they buy:**

| Tier | What it is | Delivery | R2/R3 intact? | Call |
|---|---|---|---|---|
| **Browser** | The desktop browser, any OS — under Path D, the RN-for-Web build | The existing Render deploy | **Yes, trivially.** It is a web deploy; there is no store in the path | **Take.** This is `13` D8's substance preserved |
| **iPad** | The desk shell in the native binary, at iPad width | App Store + EAS Update | **Yes** — same binary, same channel, same runtime version as iPhone | **Take** (that is the R4 decision) |
| **Mac (Apple Silicon)** | The same iPad binary made available on Macs — a distribution setting, not a build target ⚠︎ | App Store, one checkbox | **Yes** — same binary, same channel | **Investigate at Spike C.** Free if it works; the caveats are §4h's four, on a machine where they matter more |
| **Packaged desktop** | A genuine macOS/Windows app — `react-native-macos`, Catalyst, or Electron ⚠︎ | Mac App Store, or a self-hosted installer + updater | **No** | **Decline** |

**The structural point: the two tiers actually worth having are the two that keep the requirement.**
Browser and iPad both preserve instant updates completely. The only tier that damages them is the
packaged desktop, and it is the only one being declined — which is not a coincidence but the
reason for the ranking. `13` D8's original argument also still holds on its own terms: packaging
buys no part of the experience `13` describes.

**Where R2/R3 genuinely get harder once the desk is in scope.** Four items, none fatal:

1. **Three channels, three latencies.** Today: one deploy, one artifact (§2a). After Path D: a web
   deploy (seconds) for the browser tier and the periphery, an EAS Update (next launch — §5's
   timing nuance) for iPhone and iPad, and a store build (days) for anything native. A change to
   the *shared shell* now lands on the browser immediately and on devices later. That is tolerable
   and it must be *known*, because "shipped" stops being a single moment.
2. **Two web bundles to keep in step.** Under Path D the RN-for-Web app shell and the DOM
   periphery are separate builds from one repo, and they must not be allowed to disagree about the
   session or the wire contract. Easy, but only if it is a rule from the first day.
3. **The native surface must be frozen early — and the desk adds to it.** §5's right-hand column
   is the list to keep short, and Path D adds hardware-keyboard handling and possibly pointer
   support to it. Per §6 S2 and §8 R7, declare them in the *first* build even though the desk
   shell will not exist for months. A native capability added later costs a review cycle **and**
   fences part of the fleet off from future OTAs.
4. **Skew gets a third dimension.** §8 R3 already flags frontend/backend skew. The desk adds a
   browser tier that is always newest against native tiers that are not, so the same backend now
   faces up to three client vintages. It does not change R3's recommendation (additive-only wire
   changes plus a minimum-client-version check) — it raises the price of ignoring it.

**What does *not* get harder, and is worth saying because it is counter-intuitive:** adding iPad
costs nothing in update mechanism. It is the same binary, the same runtime version, the same EAS
channel and the same review as iPhone. Under Path D the desk is *cheaper* to keep current than it
is today, because the browser tier and the native tiers ship from one source.

---

## 6. The migration, in stages

Each stage is independently valuable and independently abandonable. Nothing after S0 is committed
by starting S0.

### S0 — Decisions and prerequisites *(no code)*

Answer §9's open questions. Enrol in the Apple Developer Program (⚠︎ ~$99/yr), create the App ID,
APNs key, and bundle identifier. Confirm EAS plan and pricing ⚠︎. **Exit:** §9 has no unanswered
Q1–Q4. ⊕ **Add Q8 to that exit** — what iPad portrait renders is a `13` question, it needs `13`'s
authors rather than an implementation ticket, and it is the kind of thing that is cheap to settle
in a conversation now and expensive to settle in code at S7. **Q7 and Q9 are deliberately *not*
S0 exits**: Q7 is what Spike C is for, and Q9 should be decided with an RN desk shell in hand
rather than in the abstract.

### S1 — ⊕ Three spikes, in parallel, timeboxed *(throwaway code, in a scratch worktree)*

- **Spike A (the one that matters): voice.** Options 1 and 2 from §4c, head to head, on a real
  device. Success criterion: 16 kHz mono PCM16 frames reaching AssemblyAI's socket with
  `pcm-batch.ts` reused unmodified, and a clean stop/teardown. **If both fail, the plan changes** —
  §4c option 3 becomes the design and that is a different document.
- **Spike B: auth.** GitHub App flow through `expo-web-browser`, ending with an authenticated
  `GET /api/me`. Determines §9 Q1 (cookie vs. bearer) with evidence.
- ⊕ **Spike C: the desk in RN.** `DesktopScreenView`'s arrangement — rail, working strip, feed,
  right-edge overlay with its scrim — rendered in React Native on a real iPad with a keyboard and
  trackpad attached, and in a browser via RN-for-Web. It is not building the shell; it is answering
  §4h's four exposures: does hover-reveal work on iPad with a pointer, can roving keyboard focus be
  made real, does the working strip hold its height, does the overlay behave. While the iPad build
  exists, also try the Mac (Apple Silicon) tier from §5b's table — it costs an afternoon and could
  settle the Mac question for years.

  **Spike C gates Path D and S7 only. It does not gate S2–S6**, which are Path C and are worth
  doing whatever Spike C says. Running it in S1 rather than later is a scheduling convenience, not
  a dependency: the answer changes what the *destination* is, and it is better to know that while
  the mobile shell is still being designed than after.

**Exit:** a written recommendation on each. This is the only stage that can invalidate the plan, so
it comes before anything is built. ⊕ Precisely: **Spike A can invalidate the document; Spike C can
only invalidate Path D**, and its failure mode is a `13` amendment (§4h.1) rather than a dead end.

### S2 — The Expo app exists and installs

A minimal Expo project in the repo (`/mobile`, alongside `/frontend` — see §9 Q2 for the monorepo
question). One screen. EAS Build producing an installable `.ipa`; EAS Submit to TestFlight. Icon,
splash, bundle id, permissions declared **now** rather than later, because every one of them is on
the review side of §5's line and you want them settled before the cadence matters.

⊕ **This stage owns the device-family decision (R4), and it is the cheapest moment to make it.**
The options, and why the recommendation is what it is:

| Option | What iPad users get | Verdict |
|---|---|---|
| **iPhone-only device family** | The scaled iPhone app in compatibility mode, or the browser — which today gives them the desk shell, correctly | **Recommended for the first submission.** iPhone → Universal is a widening and is allowed later; the reverse is the one-way door §8 R7 is about. This is the reversible direction |
| **Universal, iPad renders the phone shell** | A phone screen on a 13-inch display | **No.** It contradicts `13` outright and is the shape guideline 4.2 looks at hardest |
| **Universal, iPad renders the desk shell in a WebView** | The real desk experience, in a web view | **No.** It puts the *daily* surface in a WebView, which is the exact line §3c says not to cross |
| **Universal, iPad renders the desk shell in RN** | The real thing | **Yes — and it is Path D / S7.** Not available at S2 because the shell does not exist yet |

So: **declare iPhone-only at S2, and widen to Universal at S7 when there is a desk shell to widen
into.** This is a sequencing choice, not a scope reduction — R4 is met by the plan covering iPad
properly, not by the first binary shipping to it. If the user would rather have iPad in the first
submission, the honest cost is that S7 moves in front of S5 and the whole schedule waits on a shell
family nobody has spiked yet; that trade is available and is not recommended.

Declare the desk's native prerequisites here regardless — hardware-keyboard handling and pointer
support (§5b.3) — since they are entitlement-and-capability shaped and cost nothing unused.

**Exit:** the app is on the user's phone, from TestFlight, and it opens. R1 is met — with a
placeholder inside it. ⊕ The device family is decided and recorded.

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

⊕ *The middle clause of that first sentence is Path C's position and is superseded by S7 if Path D
is taken — under Path D the desktop entry point stops being the DOM app and becomes the RN-for-Web
build of the same shell. The rest of S6 is unaffected.*

### ⊕ S7 — Path D: the desk joins the app *(gated on Spike C; a separate decision, not a continuation)*

Only entered if Spike C came back clean and the user still wants iPad and desktop under one native
approach. Everything it needs already exists by this point — the RN toolchain, the L1–L3 sharing
from S4, the conformance adapter from S5, EAS Build/Update, APNs. **S7 adds no new mechanism, only
a fifth shell and a third rendering target.**

In dependency order:

1. **RN-for-Web builds at all.** Stand the web target up against the *existing* native mobile
   shell first, so the renderer is proven before a new shell is riding on it. Nothing ships.
2. **The desk shell as a fifth L4 shell** — rail, working strip, feed, right-edge overlay,
   composer — over the same L1–L3, with §4h's four exposures each explicitly asserted rather than
   assumed. **Named deliverable, on the S5 precedent: the desk shell joins
   `feed-shell-conformance.test.tsx` as a row in `SHELLS`, in the same change that lands it.**
3. **Widen the device family to Universal** and ship the desk shell to iPad. This is the R4
   payoff and the first user-visible moment of S7.
4. **Cut the desktop browser over** to the RN-for-Web build, and retire `PrimaryScreenView`,
   `DesktopScreenView`, `KanbanScreenView` and their CSS. The web **periphery** is untouched and
   stays DOM permanently.
5. **Amend `13` D8** (§7) — as a documentation deliverable of this stage, not a preamble to it.

**Exit:** one shell source per screen family, rendered on iPhone, iPad and the desktop browser;
the conformance suite asserting all of them agree; `13` amended to match what shipped.

**Explicitly reversible until step 4.** Steps 1–3 add a target without removing one; the DOM
desktop app is still there and still shipping. Step 4 is the point of no return, and it should be
taken only once the RN desk shell has been the iPad experience long enough to trust.

---

## 7. Does this actually meet R1–R5? ⊕

| | Met? | With what caveat |
|---|---|---|
| **R1** — a real installable iPhone app | **Yes**, at S2 | Via TestFlight first; App Store only when the product is meant for others |
| **R2** — frontend changes live immediately | **Yes**, at S3 | With a real nuance: applied on **next app launch** by default, not on the spot (§5). Mid-session reload is available and is a deliberate product choice, not a default |
| **R3** — App Review not a day-to-day bottleneck | **Yes** | Provided the native surface stays small and stable. §5's right-hand column is the list to keep short — and it is the reason Path C beats Path B, which would put the entire UI's native dependencies on that side of the line |
| ⊕ **R4** — not iPhone-only; iPad accounted for | **Yes, by the plan; at S7 by the binary** | The plan now covers iPad properly (§2f, Path D, S7) and the first submission is deliberately iPhone-only because that is the widenable direction (§6 S2). If "met" is read as "the first build ships to iPad", it is **not** met and S7 moves in front of S5 — see S2's table for that trade |
| ⊕ **R5** — desktop under the same native approach, R2/R3 intact | **Yes**, at S7 | The browser tier keeps instant updates trivially (it is a web deploy) and iPad shares iPhone's channel exactly. The only tier that would break R2/R3 is a packaged Mac app, and §5b declines it. Cost: RN-for-Web becomes a third rendering target (§8 R9) |

⊕ **The one honest caveat across R4/R5.** Both are met by a plan whose desk half is *gated on a
spike that has not run*. That is the same footing R1–R3 are on — the whole document is gated on
Spike A — but it is worth not eliding: if Spike C says hover-reveal and keyboard navigation cannot
be made good on iPad, R4 is met by amending `13` rather than by delivering `13`, and that is a
different answer. §9 Q7 is where that gets decided.

---

## ⊕ 7b. What this asks of spec `13`

`13` **D8** reads: *"Desktop is the responsive web app widening out, not a separate installable
application"* — rejecting packaging because *"every behavior above works in a browser window…
Packaging, auto-update, and window-chrome work would buy no part of the experience described
here."*

That reasoning is **still correct about macOS**, and this document agrees with it: the packaged
desktop tier is declined (§5b). What D8 did not consider is **iPad** — where the desk experience
has to live inside an installable binary regardless, because that is what an iPad app is.

**Proposed amendment, to be recorded in `13` §14 if Path D is accepted at S7:**

> **D8a — amends D8 (2026-08-09).** D8's rejection of a packaged *desktop* application stands, and
> for its original reasons; a Mac/Windows binary is still declined, now with a second reason — it
> is the only delivery tier that reintroduces a review gate or a bespoke update pipeline, and
> instant cloud updates are load-bearing. What changes is that the desktop **experience** is no
> longer browser-only: because an iOS app runs on iPad and Kiln renders the desk shell at iPad
> width (`DESKTOP_MIN_WIDTH = 1024`), the desk shell must exist inside the native binary. Desktop
> is therefore delivered as **one React Native shell rendered three ways** — natively on iPad,
> natively at phone size alongside it, and in the desktop browser via React Native for Web. The
> browser remains a first-class desktop target and loses nothing; what changes is the renderer
> underneath it, not the delivery.

Two smaller notes for the same amendment pass:

- **`13` §11's *"Frontend only, in the main"*** stops being true under this plan — §4g's APNs
  sender, §9 Q1's possible bearer session, and §9 Q3's SSE replacement are all backend work.
- **`13` §13 Q4 / D10** are untouched and *load-bearing in the other direction*: the four-layer
  split is the reason the desk leg is 1,604 view lines rather than a rewrite (§2f).

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
annoying afterwards. Declare the full permission set in S2 even if unused, per §6. ⊕ **Device
family is the same shape of decision and is why S2 recommends iPhone-only**: widening to Universal
later is routine, narrowing after iPad users exist is not.

**⊕ R8 — The desk's defining affordances are React Native's weakest ones — but less so than they
look.** §4h. Hover-reveal and keyboard navigation are what `13` §6/§9 build the ambient design out
of, and they are what RN handles least well on the one device — iPad with a pointer — that Path D
exists to serve. **The audit downgraded this risk mid-writing and the reason is worth keeping:**
every hover-reveal in the desk shell is already paired with `:focus-within`, and the roving focus
behind it is already JS rather than CSS, so `13` never depended on hover alone and the port
inherits a working second path. What remains is narrower — expressing focus-driven reveal in RN,
and hardware-keyboard delivery on iPad — and losing hover outright is now a degradation rather
than a failure. Still the risk most specific to widening scope, still answered by Spike C in S1
rather than discovered in S7; no longer the one most likely to end Path D.

**⊕ R9 — A third rendering target, permanently.** Path C ends with two shells (DOM web, RN
native) over one contract. Path D ends with **one shell source rendered three ways** — RN iOS at
phone size, RN iOS at tablet size, RN-for-Web in a browser. That is strictly better for drift
(§8 R2 stops doubling) and strictly worse for surface area: every bug report needs a target, the
`make check` gate needs to cover a renderer that jsdom cannot model, and the browser rendering —
the one this repo currently gets for free and for nothing — becomes something maintained. Note
this trades a *drift* risk for a *fidelity* risk; the first compounds silently and the second does
not, which is why the trade is worth making, but it is a trade.

**⊕ R10 — Retiring a working desktop app to render the same design differently.** Step 4 of S7
deletes `DesktopScreenView` and its CSS to replace them with an RN-for-Web rendering of the same
`13` design. The user-visible upside of that specific step is **zero** — the entire benefit is not
maintaining two desk shells. That is a good reason to do it and a bad reason to rush it, and it is
why S7 step 4 is explicitly the point of no return and explicitly last.

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

**⊕ Q7 — Can the desk shell be React Native without losing what makes it the desk?** §4h, decided
by Spike C. In priority order: the focus-driven reveal expressed in RN, hardware-keyboard delivery
on iPad, hover on iPad with a pointer, then the sticky strip and the right-edge overlay.
*Leaning — cautiously yes, and more confidently than when this revision started.* The reveals are
already dual-path (`:focus-within` beside every `:hover`, seven rules of it) and the roving focus
is already component logic, so the port is re-expressing a mechanism rather than inventing one. If
the answer is still no, the follow-on is whether `13` §6 takes a pointer-free answer (long-press,
or ages always visible) or whether iPad keeps the browser.

**⊕ Q8 — What does iPad *portrait* render?** §2f. Today's 1024px breakpoint gives portrait the
phone shell on mini/Air/11-inch and the desk shell on the 13-inch Pro, so inside an installed app
rotating the device would swap products — on some iPads and not others. Options: lower the
breakpoint so iPad is always the desk; keep the split and make the transition deliberate; or give
the desk shell a narrow arrangement. *Leaning: iPad is always the desk, on the grounds that which
product you are using should not depend on how you are holding the device, and certainly not on
which iPad you bought.* This is a `13` question that only becomes urgent because of this plan, and
it should be settled with `13`'s authors rather than in an implementation ticket.

**⊕ Q9 — Does the DOM app shell actually get retired, or do both live?** S7 step 4. Keeping both
is the S6/Q5 answer applied to the desk, and it has the same appeal (free fallback) and a worse
version of the same cost — it is §8 R2's drift, on the shell family that just had a whole
architecture plan written to stop it drifting. *Leaning: retire it, but only after the RN desk
shell has been iPad's experience for long enough to trust — and note this is the one decision in
the document that is genuinely irreversible.*

---

## 10. Recommendation

⊕ *Revised for scope. The first four items are the original recommendation and are unchanged; the
last three are what widening to iPad and the desk adds.*

**Adopt Path C.** Ship a real Expo iPhone app whose primary screen is native React Native over the
existing L1–L3 layers, with the peripheral configuration screens served as web inside the app, and
the desktop/kanban web experience untouched *for now*.

**Do S0 and S1 first and treat them as a gate.** The voice spike is the one result that can
invalidate this document, and it costs days to find out rather than months.

**Do S3 — the update pipeline — before S5, the real UI.** Proving R2 and R3 on a trivial app means
that when the real screen is the unknown, the pipeline is not.

**Raise the version-skew question (R3, §8) as its own ticket.** It is the finding in here least
likely to be noticed by anyone else and the most likely to bite, because it quietly retires an
assumption that `AGENTS.md` currently states as policy.

⊕ **Adopt Path D as the destination, and S7 as the way there — but gate it on Spike C, not on
enthusiasm.** iPad forces the desk shell into React Native (§2f); once it is there, keeping a
second desk shell in the DOM is the drift problem this repo already wrote an architecture plan to
solve, so RN-for-Web serves the browser from the same source. The leg is smaller than the mobile
one (1,604 view lines, 465 logic, 1,580 CSS) and needs no mechanism Path C has not already built.

⊕ **Declare the first submission iPhone-only, and widen to Universal at S7.** This is sequencing,
not scope: R4 is met by the plan covering iPad properly, and iPhone → Universal is the direction
that stays open. Shipping iPad in the first binary is available and costs the schedule S7 in front
of S5 — see §6 S2's table. If the user wants iPad early, that is the trade to make knowingly.

⊕ **Add Spike C to S1 and run it beside Spike A.** It is the only new *kind* of risk the wider
scope introduces (§8 R8): the desk's hover-reveal and keyboard navigation are what `13` §6/§9 are
built on, and they are what React Native handles worst on iPad-with-a-pointer. Everything else in
Path D is more of work already understood. Unlike Spike A, its failure does not kill the document
— it turns Q7 into a `13` amendment, which is a decision the user should get to make with evidence
rather than a surprise found in S7. **Note the audit already moved this risk downward**: the desk
shell pairs `:focus-within` with every `:hover` and drives roving focus from component logic, so
`13` was never hover-only and the spike is confirming a port rather than probing a hole.

What this document does **not** do is start any of it. Per the ticket — the original and the
revision — no Expo scaffold exists, no dependency was added, and no application code was touched.
