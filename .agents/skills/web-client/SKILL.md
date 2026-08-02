---
name: web-client
description: Use when working in the frontend — the thin, disposable, mobile-first client. The primary screen (08) is a feed + dock over a live board; voice (STT, 09) and Web Push notifications (10) have shipped, and Kiln does not speak (TTS was cancelled). A landing page, sign-in, and per-user projects (11) are now in front of it. Holds no authoritative state. Anchor /frontend. Specs 02 §11, 07, 08.
---

# Web client (02 §11, v1 shape decided by 07)

## Functional Requirements

**Responsibility.** A deliberately thin, disposable, mobile-first surface that renders the
board live and carries the conversation with the brain. The 07 "board + text chat" is now the
`/debug` developer view; the user-facing **primary screen (08)** is a feed + dock. Voice
(02 §9, STT in front of `POST /api/message`) and Web Push notifications (02 §10) have both
**shipped** — the earlier "deferred to 09/10" framing is closed. **There is no TTS: Kiln does
not speak** (the old "TTS on top of `say`" is cancelled; `say` is on-screen text only).
**Holds no authoritative state.**

**Open decisions — resolved in `docs/specs/07-v1-text-client.md` (status: proposed).**
- [x] Framework/build → 07 §5: Vite + React + TS strict (escape-hatch bans per 02 §4b);
      types generated from /schema via openapi-typescript; **dependencies gated on explicit
      user approval, default zero** (D4 — not a flat ban: block on the user before adding
      any new module/library, then it's allowed; approved so far: `vaul` (proposal sheet),
      `react-markdown` + `remark-gfm` (feed/message rendering), `@sentry/react`,
      `react-router-dom`, and the `@fontsource-variable/*` fonts) — two contexts (board store: wholesale
      snapshot replacement; chat store: fetched page + say events + optimistic sends
      reconciled by message_id).
- [x] Transport → SSE + POST (04 D6): one thin module wraps EventSource + fetch — the
      only code that knows URLs.
- [x] Endpoints (07 §4, amending 04 A1): the 07 core seam — `GET /api/stream`
      (now **four** SSE events: `board`, `say`, `feed`, `activity`), `GET /api/board`,
      `POST /api/message {text}` → 202, `GET /api/messages?limit` — is now a subset of a
      larger surface (feed: `GET /api/feed`/`history`, `POST /api/feed/seen`/`dismiss-all`/
      `{id}/dismiss`; `GET /api/activity`; `POST /api/tickets/{id}/accept|delete`; push;
      voice; identity). `transport.ts` is still the only code that knows URLs.
- [x] Rendering: **a multi-route SPA** (React Router in `main.tsx`), not one screen —
      `/` is the marketing landing (`Landing2`; installed web apps redirect to `/app`),
      `/app` is the **primary screen (08)** = feed + dock over the board (behind the
      `SessionGate`), `/debug` preserves the original 07 board-on-top / chat-below layout
      (`App.tsx`), `/dashboard/*` is the account view, `*` is a 404. Board is read-only —
      **no drag-and-drop**; all mutation flows through the brain (D5).
- [x] Reconnection → 07 §8: EventSource native retry; first board event = resync; one feed
      refetch per stream reopen; stale-but-visible dimming, never blank.
- [ ] PWA vs wrapped-native — deliberately deferred (07 D2).

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

**Permanent error band vs. toasts (`SystemAlertBand`).** The dock region hosts one
*persistent* surface that is the deliberate opposite of the toast overlay: a permanent
error band (`[data-role='system-alert-band']`, `role="alert"`) rendered **in flow** at
the top of `[data-role='dock-region']`, above the dock. It is driven by the board
snapshot's `alerts: SystemAlert[]` (wholesale-replaced like the rest of the board), so it
persists across snapshots until the condition clears and then vanishes. Two rules make it
behave: (1) it is **in flow**, not an upward-growing overlay — a permanent problem should
*reserve* its own height, not float over and hide the feed the way an auto-dismissing
toast does; (2) `SystemAlertBand` returns `null` for an empty `alerts` array, so the
healthy layout (and every snapshot test) is byte-for-byte unchanged. Keep it
**error-agnostic**: it renders each alert's `detail` verbatim and never switches on `kind`
(server-composed copy, e.g. "2 of 5 sandboxes failing"), so the same band serves any
future persistent failure, not just sandbox health. The server side lives in the api board
join (`agentJoin`/`sandboxHealthAlerts` in `internal/api/routes.go`), derived from the
neutral per-worker `AgentStatus` (`errored`), so it stays provider-agnostic.

## Dashboard + session gating (spec 11)

A second, separate surface at `/dashboard` — the signed-in account view (GitHub sign-in →
first-run project onboarding → settings with credentials + live verify). It owns its own
`DashboardProvider`; the primary screen never mounts it. **Phase 2 put the whole app behind a
session** — `main.tsx` wraps **both `/app` and `/debug`** in `SessionProvider` + `SessionGate`
(the gate resolves `GET /api/me` before either mounts), because every `/api/*` call is now
project-scoped. Only the public routes — the landing (`/`), onboarding, and beta-thanks — stay
session-free.

- `src/dashboard/` reuses the store/context split from `src/stores/`: `dashboard-store.tsx`
  (the provider + all mutation methods — `saveProject`, `saveSettings`, `runVerify`,
  `signOut`) and `dashboard-context.ts` (the bare `useDashboardStore` hook) as two files,
  same reason as `board`/`chat`/`feed` — the hook file has no JSX so components importing
  only the hook don't drag the provider's implementation into their module graph.
  `Dashboard.tsx` switches on the store's `phase` (`loading`/`signed-out`/`ready`) and
  `me.project` to pick `SignIn`/`Onboarding`/`Settings`; `ConfigFields.tsx` holds the
  `ProjectFields` form shared between Onboarding and Settings.
- **Credentials are connect cards, not fields** (`Integrations.tsx`). Settings renders one
  `[data-role="integration-card"]` per provider — GitHub, Amika, Devin (Anthropic only when
  `VITE_SHOW_ANTHROPIC_KEY_FIELD=1`). **There is no free-text token input anywhere**; the old
  flat `CredentialFields` form is gone. Amika/Devin connect through a small modal
  (`[data-role="api-key-modal"]`) whose single input sends just that one field, so a save still
  chains a verify run and updates the card's `credential-status` mark. GitHub connects through
  the existing OAuth route — `Connect`, and once connected a right-aligned `Switch account`
  anchor in the card's `[data-role="integration-action"]` slot, both plain full-page links to
  `/auth/github/login`. Secrets stay write-only: the modal input never seeds from the stored
  value, only a `configured · …tail` placeholder.
- **Caveat inherited from that redesign:** `user_config.github_auth_token` (the PAT the repo
  paths use — private clones, `gh` PR gate, sandbox push) no longer has a UI entry point.
  Sign-in OAuth requests **no scopes** and never persists its token, so a GitHub card reading
  "Connected" does **not** imply a stored repo credential. `PUT /api/settings` still accepts
  `github_auth_token`, so already-stored tokens keep working and the field is settable by API.
- `vite.config.ts` proxies `/auth` to the backend alongside `/api` and `/api/stream` — the
  GitHub OAuth redirect (`GET /auth/github/login` → `/callback`) needs to hit the backend
  directly, not be intercepted by the SPA's client-side router.

### The repo picker — sign-in IS the GitHub connection

`ProjectFields` has **no free-text repo URL field**. The repo is picked from the connected
GitHub account (`RepoField` in `ConfigFields.tsx`, fed by `useGitHubRepos` →
`GET /api/github/repos`). The load-bearing decision: there is **one** OAuth flow and **one**
registered callback — the sign-in authorize URL requests the `repo` scope, and
`CompleteLogin` keeps the resulting token as the user's GitHub credential (the same slot a
hand-entered PAT uses). So "Connect GitHub account" is just `/auth/github/login` again.
**Never add a second OAuth app, flow, or callback for repo access.**

- `useGitHubRepos` is **user-scoped**, unlike `useSandboxCatalog` (per-project, because each
  project resolves its own agent provider). Mount it once per view — `Settings`,
  `ProjectsBody`, `Onboarding` — and pass it down; one credential serves every project card.
- Three render states, and the ordering matters: `loading` shows a placeholder (so the
  connect prompt never flashes before we know), `!connected` shows the connect link,
  connected shows the dropdown. `connected: false` is a normal 200, **not** an error — a
  failed *request* sets `error` instead and deliberately leaves `connected` alone, so a
  transient blip can't demote a working picker mid-edit.
- **The dropdown has no filter box beside it** (removed with the project modal): a native
  select already types-to-jump, and a second search control next to it only raised the
  question of which one to use. Don't reintroduce one at "enough repos" — if long lists ever
  need more, replace the select, don't grow a sidecar.
- An existing `repo_url` preselects via `sameRepo`, which normalizes case, a trailing `/`,
  and `.git` — hand-typed values predate the picker and carry all three. An unmatched value
  stays selectable as `(current)` and is still submitted, so editing an unrelated field can
  never silently unlink a project's repo. Same guarantee the snapshot picker makes.
- **Every `ProjectFields` render site and test must pass `github`** — it's a required prop on
  purpose, so no free-text fallback path can drift back in.

### The settings page is desktop-first (settings redesign)

`/dashboard`'s settings view is the one surface in this repo that is **not** mobile-first — it's
a management page visited from a laptop. `Settings.tsx` is a two-column shell (sticky section
nav + a column of section cards: Account / Integrations / Notifications / Projects), and
`Dashboard.css` keeps every control compact: 14px body, 12.5px labels and buttons, ~32px
inputs, a 2–3-column project-form grid and a 2-column credential grid.

- **The nav scrolls to sections; it does NOT swap panes.** Every field stays mounted. That is
  what keeps find-in-page working, keeps a section deep link from hiding the rest of the page,
  and — load-bearing for the suite — keeps `dashboard-config.spec.ts`'s
  `getByLabel('Amika API key')` reachable on load instead of behind a tab it would have to
  discover. If you add a section, add it to `SECTIONS` in `Settings.tsx` (one source of truth
  for the nav and the headers) and keep the pane mounted.
- **Know which config component you're touching.** `CredentialFields` is **Settings-only**, so
  it's free to restructure. `ProjectFields` is shared by **three** surfaces (Settings,
  `ProjectsManager`, Onboarding) — restyle it from `Dashboard.css` (everything there is scoped
  under `[data-role='settings']`/`[data-role='dashboard']`, so the app-native projects page is
  untouched), don't reshape its DOM.
- **Icons decorate, they never replace a label** — hand-rolled inline SVGs in
  `src/dashboard/icons.tsx` (no icon library; deps are gated on approval per D4). They carry no
  `width`/`height`: one rule, `[data-role='settings'] svg[data-icon]`, sizes the whole tree in
  `em`. Drop that rule and every icon renders at the SVG default 300×150. All are
  `aria-hidden`, so accessible names come only from the visible text.
- **Layout-critical CSS is asserted as a string** (`Dashboard.desktop-layout.test.ts`, the
  `?raw` technique) — jsdom does no layout, so without it the page could silently revert to one
  mobile-style column and every DOM test would still pass.

### Projects: a panel list over a detail modal

The Projects section lists **one compact panel per project** (`ProjectPanel` in `Settings.tsx`
— name, `owner/name` repo, worker/agent chips) and opens the whole configuration in a dialog
(`ProjectModal.tsx`). This is the page's **one deliberate exception** to "the nav scrolls, it
never swaps panes": that rule is about the page's *sections* staying findable at once, and N
inline project forms are exactly what made the list unscannable. "New project" opens the same
dialog in create mode — `openProjectId` holds a project id or the `'new'` sentinel.

- **`ProjectFields` now has a `layout` prop.** `form` (default) is the flat field list
  Onboarding and `ProjectsManager` have always rendered — **its DOM must not move**, those
  surfaces style it themselves. `detail` is the modal's shell: an identity header (name edited
  in place + the repo picker, together — there is no raw URL field anywhere) over grouped
  Agent and Sandbox sections. Both branches render the *same* field elements, built once above
  the branch, so the two shells can't drift in what they render or submit.
- **`SandboxInfo`** (in `ConfigFields.tsx`) reads the snapshot choice back in words, in four
  states (`data-state`: `no-catalog` / `default` / `snapshot` / `unlisted`). Keep it
  provider-neutral outside the catalog case — a provider that manages its own sandboxes gets
  the `no-catalog` reading, so the group's own hint text must not promise Amika.
- **The modal is hand-rolled, not `<dialog>`.** jsdom 25 (the whole DOM suite) ships **no
  `HTMLDialogElement`** — `showModal` is `undefined` — so a native dialog would be untestable
  in the gate. `ProjectModal` therefore owns Escape, the scrim press (only one that *starts*
  on the scrim dismisses), a Tab trap, body-scroll lock, and focus in/out itself. Vaul is not
  the answer either: it's a bottom sheet for the mobile primary screen, and settings is
  desktop-first.
- **The store's project mutations resolve `boolean`** (`createProject`/`updateProject`/
  `removeProject`): they fold failures into `error` rather than rejecting, so the boolean is
  the only signal a caller has. The modal closes on `true` and stays open — with everything
  typed into it — on `false`. Don't "simplify" these back to `Promise<void>`.
- The per-project `useSandboxCatalog` now mounts **inside the open modal**, so only the
  project actually being looked at fetches its catalog. E2e that asserts on the snapshot
  picker or the dev-box capture form must click `[data-role="project-panel"]` first
  (`tests/tests/keyless-sandbox-selection.spec.ts`).

## Common footguns

- Reaching for a TS escape hatch to get past the type checker instead of fixing the schema/types.
- Holding authoritative state in the client — it is disposable and holds none.

_(Accumulate more as you work.)_

## The ticket detail sheet's one write (per-ticket sandbox option)

The sheet is read-only inspection over a read-only board (D5) for the ticket's *content*, and
Accept / Delete / Poke all express intent that the caller routes **through the brain**. The
one exception is the **sandbox switch** (`onSetKeepSandbox` → `setTicketSandbox` → `POST
/api/tickets/{id}/sandbox`): saving a ticket's sandbox stops the board recycling its worker,
so an agent can keep working in the same workspace across turns. It is a *setting on the
ticket*, not a board transition, so it writes directly — round-tripping a toggle through an
LLM pass would be slow and non-deterministic for no gain.

Two consequences worth keeping when you touch it:

- **It does not close the sheet.** Every other action closes on tap; a setting is flipped
  while reading, so `PrimaryScreenView` passes this one straight through without
  `closeTicket()`.
- **The switch is optimistically controlled, time-boxed.** The value lives on the board
  snapshot, which only comes back over the stream, so the switch renders `pendingKeep ??
  ticket.keep_sandbox` and drops the overlay as soon as the snapshot agrees — or after
  `SANDBOX_OPTIMISTIC_MS` if the write never lands, the same self-healing shape the feed
  store's optimistic card hides use. `PrimaryScreen` also refreshes the board on a failed
  write so it snaps back at once instead of on the time-box.

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

- **A wrapping `<label>` absorbs everything inside it into the field's accessible name.** The
  dashboard's credential inputs each sit beside a live validity glyph; while the glyph was
  inside the label, `getByLabel('Amika API key')` worked right up until the first ✓ rendered,
  then silently stopped matching (in Vitest *and* in Playwright). Fix, and the rule for any new
  field with an adornment: wire the label with `htmlFor`/`id` and keep only the label text
  inside it.

- **jsdom ships no `IntersectionObserver`** — the settings nav's scroll-spy guards with
  `typeof IntersectionObserver === 'undefined'` and degrades to a static first-section
  highlight. To test the observing path, `vi.stubGlobal` a fake, and capture its callback on a
  **holder object** (`const spy = { fire: null }`), not a bare `let`: TypeScript keeps the
  `null`-narrowing of a local across an assignment made inside a nested class constructor, so
  `fire?.(...)` type-checks as `never`.

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

- **A `max-height` flex column shrinks EVERY item, not just the scrolling one.** The
  ticket sheet caps at `85dvh` and clips; flexbox removes the overflow in proportion to
  (shrink factor × flex base size), so with the default factor the dock lost height to a
  long ticket body and the live transcript inside it clipped its own text behind an
  invisible scroll (touch draws no scrollbar) — the "transcript is cut off" bug. Fix is
  shrink *priority*, not a bigger cap: the scrolling body carries an outsized
  `flex-shrink` (`flex: 1 100 auto`) so it yields first, while the dock keeps
  `flex-shrink: 1` + `min-height: 0` as a last-resort valve so its controls can't be
  clipped off a very short viewport. Same trap applies to any other capped sheet.
  Regression-tested in `TicketDetail.transcript-space.test.ts`.

- **Assert layout-critical CSS by importing the stylesheet as a string** (`?raw`, typed via
  `vite/client`) and matching the rule body — jsdom does no layout, so nothing else in the
  gate can catch a geometry regression. See `TicketDetail.safe-area.test.ts` and
  `TicketDetail.transcript-space.test.ts`.

- **Viewport units: match the unit the container already uses.** On mobile Safari `vh` is
  the *large* viewport (browser chrome hidden), so a `45vh` child inside an `85dvh` parent
  can claim more than it looks like it does.

_(Accumulate: non-obvious traps and edge cases.)_
