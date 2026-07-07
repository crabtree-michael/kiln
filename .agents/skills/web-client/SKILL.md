---
name: web-client
description: Use when working in the frontend — the thin, disposable, mobile-first client. v1 (spec 07): board visualization + text chat with the brain; voice and notifications deferred to 09/10. Holds no authoritative state. Anchor /frontend. Specs 02 §11, 07.
---

# Web client (02 §11, v1 shape decided by 07)

## Functional Requirements

**Responsibility.** A deliberately thin, disposable, mobile-first surface. **v1 (07)**:
renders the board live and holds a text conversation with the brain — voice (02 §9) and
notifications (02 §10) are deferred; they wrap these same seams later (STT in front of
POST /api/message, TTS on top of `say`). **Holds no authoritative state.**

**Open decisions — resolved in `docs/specs/07-v1-text-client.md` (status: proposed).**
- [x] Framework/build → 07 §5: Vite + React + TS strict (escape-hatch bans per 02 §4b);
      types generated from /schema via openapi-typescript; **dependencies gated on explicit
      user approval, default zero** (D4 — not a flat ban: block on the user before adding
      any new module/library, then it's allowed; `vaul` is the first approved one, for the
      proposal sheet's native slide + overscroll) — two contexts (board store: wholesale
      snapshot replacement; chat store: fetched page + say events + optimistic sends
      reconciled by message_id).
- [x] Transport → SSE + POST (04 D6): one thin module wraps EventSource + fetch — the
      only code that knows URLs.
- [x] Endpoints (07 §4, amending 04 A1): GET /api/stream (`board` + `say` events),
      GET /api/board, POST /api/message {text} → 202, GET /api/messages?limit.
- [x] Rendering → 07 §7: one screen — board on top (Backlog[Shaping/Ready],
      Developing[**Blocked above Working**], Done; capacity chip; Ready in exact pull
      order), chat panel below. Blocked cards are the loudest surface (no push in v1).
      Board is read-only — **no drag-and-drop**; all mutation flows through the brain
      (D5).
- [x] Reconnection → 07 §8: EventSource native retry; first board event = resync; one
      /api/messages refetch per stream reopen; stale-but-visible dimming, never blank.
- [ ] PWA vs wrapped-native — deliberately deferred to 09/10 (07 D2).

## How to work here

**TS escape hatches are banned** (`any`, `as`, `@ts-ignore`, non-null `!`, unused symbols) —
the hard gate enforces it (§4b). Types come from the wire schema; never hand-write the
client↔server types.

- Image-snapshot targets (02 §4a, 07 §9): TicketCard (all five states + long blocked
  reason), BoardColumn (zone stacking), ChatPanel (user/kiln/pending/failed), capacity
  chip, full mobile layout.
- The transcript is server-owned (07 §3); the chat store is a cache, not a source of
  truth.
_(Accumulate: how to run the frontend locally, build/test commands, the boundary — `/frontend`.)_

## Bottom-anchored UI layering (standing principle)

The bottom of the primary screen is a stack of layers that all grow **upward** over
the feed: the dock (mic controls, in flow) is the base; the live transcript overlays
just above it; the notification hub (toast stack / "Kiln is thinking") sits on top.

**Rule: the notification hub must never overlap the dock, and the dock is not a fixed
height** — it expands upward as the transcript grows (bounded to 28vh). So the hub is
anchored above the dock's *current* top, not its collapsed top:

- The dock publishes its transcript overlay's live height as `--dock-overlay-height`
  on the screen root (`[data-role='primary-screen']`), tracked via `ResizeObserver`
  so it updates as words stream in. It defaults to `0px` (collapsed dock).
- The hub (`[data-role='activity-row']`) offsets its `bottom` by that var:
  `bottom: calc(100% + var(--dock-overlay-height, 0px))` — `100%` clears the collapsed
  controls row, the var clears the transcript. Collapsed and expanded both stay clear.
- z-index (hub 6 > transcript 5) is only a belt-and-braces backstop for mid-resize
  frames; the geometry, not the z-order, is what keeps them from overlapping.

**When you add any new bottom-anchored surface** (another dock affordance, a second
hub, a banner): decide its place in this upward stack and anchor it to the *dynamic*
height of the layers below it (via the same var / a measured offset), never to a fixed
collapsed height that only happens to look right until the dock expands.

## Dashboard (spec 11 phase 1)

A second, separate surface at `/dashboard` — the signed-in account view (GitHub sign-in →
first-run project onboarding → settings with credentials + live verify). It owns its own
`DashboardProvider`; the primary screen at `/` never mounts it. **`/` and `/debug` stay
session-free in phase 1** — no cookie is required to use the board/chat, only to reach
`/dashboard`'s own endpoints (`/api/me`, `/api/settings`, `/api/project`, `/api/settings/verify`).

- `src/dashboard/` reuses the store/context split from `src/stores/`: `dashboard-store.tsx`
  (the provider + all mutation methods — `saveProject`, `saveSettings`, `runVerify`,
  `signOut`) and `dashboard-context.ts` (the bare `useDashboardStore` hook) as two files,
  same reason as `board`/`chat`/`feed` — the hook file has no JSX so components importing
  only the hook don't drag the provider's implementation into their module graph.
  `Dashboard.tsx` switches on the store's `phase` (`loading`/`signed-out`/`ready`) and
  `me.project` to pick `SignIn`/`Onboarding`/`Settings`; `ConfigFields.tsx` holds the two
  controlled forms (`ProjectFields`, `CredentialFields`) shared between Onboarding and
  Settings — secrets are write-only, so credential inputs never seed from the stored value,
  only a `configured · …tail` placeholder.
- `vite.config.ts` proxies `/auth` to the backend alongside `/api` and `/api/stream` — the
  GitHub OAuth redirect (`GET /auth/github/login` → `/callback`) needs to hit the backend
  directly, not be intercepted by the SPA's client-side router.

## Common footguns

- Reaching for a TS escape hatch to get past the type checker instead of fixing the schema/types.
- Holding authoritative state in the client — it is disposable and holds none.

_(Accumulate more as you work.)_

## Swipe-to-dismiss (feed cards, 08 §3)

- `SwipeToDismiss.tsx` is the reusable swipe-left-to-clear wrapper (pure pointer
  events + CSS transform, no gesture lib per D4). `PrimaryScreenView` wraps a row in
  it **only** when `onDismissCard` is wired AND the card is notification-backed
  (`updateId(card) !== null` — update/preview; blockers/proposals/pokes are board
  state the brain owns and stay static). Gating on the optional prop keeps the DOM
  (and the image snapshots) unchanged for presentational tests that don't pass it.
- The clear is full-stack and persistent: `feed-store`'s `dismissCard` optimistically
  hides the card (a `dismissedRef` set filtered in `mergeFeed`, mirroring the
  optimistic-accept `acceptedRef`), POSTs `/api/feed/{id}/dismiss` (retracts the
  notification server-side), and springs the card back if the request fails. The
  suppression is pruned in `applySnapshot` once the server-confirmed snapshot drops
  the id. A purely client-side hide would resurrect on the next snapshot/reload.
- The fling-off completion is driven by a **timer** (`FLING_MS`, matched to
  `--duration-base`), not `transitionend` — under `prefers-reduced-motion` the CSS
  transition is suppressed and `transitionend` would never fire.

## Potential gotchas

- **jsdom ships no `PointerEvent`** — `new PointerEvent(...)` throws, and
  testing-library's `fireEvent.pointer*` silently drops `clientX/clientY`, so a
  gesture reading coordinates sees `NaN`. Polyfill it in the test with a
  `MouseEvent` subclass (jsdom carries mouse coords) via
  `vi.stubGlobal('PointerEvent', Stub)` — see `SwipeToDismiss.test.tsx`. Also guard
  `setPointerCapture` with a `typeof … === 'function'` check; jsdom elements lack it.

- **The `vaul` proposal sheet (`TicketDetail.tsx`, first approved dep under the amended
  D4).** Three traps, all from it being a Radix Dialog under the hood:
  - **`data-state` is Radix's** — it writes `data-state=open|closed` on the panel/overlay
    to drive the slide animation. The ticket's own lifecycle state therefore rides on
    `data-ticket-state` (the blocked-border CSS keys off that), never `data-state`.
  - **Content portals to `document.body`**, so it leaves the `[data-role='primary-screen']`
    subtree — the primary-vs-debug skin can't key off DOM ancestry anymore; it rides on an
    explicit `data-surface` attribute on the panel. In tests, query the sheet via `screen`/
    `document`, not the render `container` (which no longer holds it).
  - **Accessible name = the `<Drawer.Title>`** (Radix wires `aria-labelledby` to it, which
    per accname *beats* any `aria-label` you also pass — so don't bother with one). The
    dialog's name is the visible ticket title, e.g. `getByRole('dialog', { name: '<title>' })`.
  - Dismissal (Escape, scrim, drag past threshold) is Vaul's, surfaced as
    `onOpenChange(false)` → our `onClose`; don't hand-roll Escape/backdrop handlers. Vaul
    renders and closes fine in jsdom (Escape works), but its drag physics don't — don't
    assert on them.

_(Accumulate: non-obvious traps and edge cases.)_
