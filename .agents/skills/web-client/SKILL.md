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

*Intent, not enforcement. The rules below are checked as computed geometry in
`tests/layout/` (part of `make check`) and the stacking order is published as a single
scale in `styles/tokens.css`. This section says what the arrangement is FOR; it is not
where the arrangement is defined, and amending it fixes nothing.*

The bottom of the primary screen is a stack of layers that all grow **upward** over
the feed: the dock (mic controls, in flow) is the base; the live transcript overlays
just above it; the notification hub (toast stack / "Kiln is thinking") sits on top.

**The dock is not a fixed height.** It expands upward as the transcript grows, so
anything anchored above it must be anchored to the dock's *current* top, not to its
collapsed one — the dock publishes its overlay's live height as `--dock-overlay-height`
and the hub offsets its `bottom` by that. Geometry is what keeps the layers apart; the
z-order is only a backstop for mid-resize frames.

**The reserve is for the CARDS, not for whatever the feed pins into it.** The feed's
`padding-bottom` grows with the overlays so the newest card can be scrolled clear of
them. That is a scroll allowance and nothing more. **A toast arriving is not a layout
event** — it must not move anything the user is looking at. "Show earlier" sits at the
top of that reserve, so it has to give the band's share back in paint and let the band
overlay it, rather than riding the growth like a lift.

**The layer order is a lookup, not a paragraph.** `--layer-*` in `styles/tokens.css`
states it once: on screen, `detail-panel > detail-scrim > popover > header > dock >
feed`; inside the dock region (which is its own stacking context, because of the
keyboard-lift transform), `hub > transcript`. Read a rung; do not write a number. The
feed's rung exists to be named, never applied — anything the feed pins into the bottom
reserve must carry no `z-index` at all, or it lifts itself over the dock's overlays.
That single mistake is the "Show earlier" / toast overlap that was fixed five times.

**When you add any new bottom-anchored surface** (another dock affordance, a second
hub, a banner): decide its place in this upward stack, anchor it to the *dynamic* height
of the layers below it, take its rung from the scale, and add the assertion to
`tests/layout/bottom-stack.spec.ts`. A rule written down here and nowhere else is not
a rule.

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

**Activity pills are one tap target, never two (`ActivityRow`, 08 §4).** No pill carries a
close control in any state — no always-on ×, and no Close button once it is open. One
`button[data-role='toast-open']` fills the pill and the tap means whatever that pill has
left to do: a board `toast` with a ticket routes to its detail view and dismisses; a pill
with nowhere to route expands in place if — and only if — its 2-line clamp is actually
hiding something, and the next tap dismisses it; a pill with nothing more to show dismisses
on the first tap rather than opening onto an identical copy of itself. "Can it expand?" is
**measured, not assumed** (`useClampOverflow`, shared with the feed card's "tap to see
more" cue), because the same utterance overflows on a phone and fits on a desk — mobile and
desktop render the same `ActivityRow`, so a hardcoded answer would be wrong on one of them.
Consequence for tests: jsdom does no layout, so every pill reads as *non*-expandable unless
you fake the heights (`fakeClampedOverflow` in `ActivityRow.test.tsx` /
`PrimaryScreenView.test.tsx`), and expanding pauses that pill's auto-dismiss timer
(`setToastExpanded`) with no collapse-back to resume it.

## Dashboard + session gating (spec 11)

A second, separate surface at `/dashboard` — the signed-in account view (GitHub sign-in →
first-run project onboarding → settings with credentials + live verify). It owns its own
`DashboardProvider`; the primary screen never mounts it. **Phase 2 put the whole app behind a
session** — `main.tsx` wraps **both `/app` and `/debug`** in `SessionProvider` + `SessionGate`
(the gate resolves `GET /api/me` before either mounts), because every `/api/*` call is now
project-scoped. Only the public routes — the landing (`/`), onboarding, and private-beta — stay
session-free. `/signup` sits beside `/dashboard` (its own provider, outside the gate): the
same sign-up flow, replayable on demand — see the rehearsal section below.

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
  the OAuth route — `Connect`, and once connected a right-aligned `Switch account`
  anchor in the card's `[data-role="integration-action"]` slot, both plain full-page links to
  `GITHUB_CONNECT_PATH`. Secrets stay write-only: the modal input never seeds from the stored
  value, only a `configured · …tail` placeholder.
- `user_config.github_auth_token` (the credential the repo paths use — private clones, `gh`
  PR gate, sandbox push) has no free-text UI entry point; the OAuth flow writes it.
  `PUT /api/settings` still accepts `github_auth_token`, so already-stored tokens keep
  working and the field is settable by API.
- `vite.config.ts` proxies `/auth` to the backend alongside `/api` and `/api/stream` — the
  GitHub OAuth redirect (`GET /auth/github/connect` → `/callback`) needs to hit the backend
  directly, not be intercepted by the SPA's client-side router.

### One GitHub flow — sign-in IS the GitHub connection

There is **one** OAuth flow, **one** registered callback, and **one** path constant:
`GITHUB_CONNECT_PATH` in `src/auth/github-connect.ts` (`/auth/github/connect`). Import it —
never restate the literal. Every entry point uses it: `landing/Landing2.tsx` ("Sign up" AND
"Log in" — see the private-beta section below),
`components/SessionGate.tsx`, `projects/ProjectsManager.tsx`, `dashboard/SignIn.tsx`,
`Onboarding`'s step 1, and the `Integrations`/`RepoField` connect affordance. It is **not**
in `integrations-config.ts` with the other shared credential facts, on purpose: the landing
page and the app's session gate link to it too, and they must not pull the dashboard's
provider tables into their bundle to do it.

**Where the flow ends is chosen by which form of the constant you link** (added 2026-08-07).
Plain `GITHUB_CONNECT_PATH` ends **in the app** (`/app`) — that is what "Sign in" means, and
the backend only diverts to `/dashboard` when the user has no project and onboarding is
genuinely next. `GITHUB_DASHBOARD_RETURN_PATH` (`?next=dashboard`) ends on the dashboard, and
is for the affordances that LIVE there — `dashboard/SignIn.tsx`, `Onboarding`'s step 1,
`RepoField`'s connect prompt — where the grant is a step in something the user is already
doing on that screen. `GITHUB_SETUP_PATH` needs no marker: the backend reads `setup=1` as the
same request, asking for GitHub's chooser being something only the dashboard does. Getting
this wrong is invisible on a phone — an installed web app relaunches at `start_url` and
`DefaultRoute` walks it to `/app` regardless — and stops a laptop dead on the wrong screen,
which is exactly how the callback's old unconditional `/dashboard` survived as long as it did.

The route redirects to the **GitHub App's authorize screen** (as of 2026-08-06), and the
backend resolves the user's installation behind it, sending anyone without one on to
GitHub's "All repositories / Only select repositories" chooser — so signing in already
authorizes repo access, to exactly the repos they picked. This replaced a split — a
scopeless `/auth/github/login` beside a repo-scoped connect — that shipped a settings card
pointed at the wrong one. **Never add a second GitHub app, flow, callback, or path constant
for repo access.** (`/auth/github/login` still 302s here for old bookmarks; nothing in the
client links to it.)

### Two buttons, one flow — and the private-beta gate behind it

The landing page's nav offers **"Sign up" and "Log in" as two separate buttons**, and they
point at the **same** `GITHUB_CONNECT_PATH`. The wording is the only difference, and that is
the design: a visitor knows which of the two they are, and a bar offering only "Sign in"
reads as closed to newcomers. **Do not give them separate routes or query markers** — 11 D2a
settled that two GitHub routes differing only invisibly is how a call site ends up pointed at
the wrong one, and "which button did they press" is not something the backend needs to know.
Both are plain `<a href>`, never router `Link`s.

**Both survive on mobile.** The bar used to keep one CTA and `display: none` the sign-in link
under 720px to save width, which left a returning user on a phone with no way in from this
page at all. Width now comes out of the buttons' padding instead (the section links have
already collapsed by then). Don't reintroduce a rule that hides either one.

**There is no email capture anywhere on the page.** `BetaSignupForm`, `BetaModal` and
`POST /api/beta-signup` are all gone, along with the `/beta/thanks` page. Signing up IS the
GitHub grant, and the beta list is now written server-side from the login (see below) — so
nobody is asked for an address they have already proved they own. Don't add a form back.

**What happens after GitHub is the backend's call, and there are three endings.** The
callback checks the allowlist (`KILN_ALLOWED_GITHUB_USERS`, 11 §2) and either lands them in
the app / on the dashboard as described above, or — if they are not on it — records their
login on the `beta_signups` list and redirects to **`/beta/pending`** (`landing/PrivateBeta.tsx`).
That screen is a **public** route, sitting outside `SessionProvider`/`SessionGate` for the
obvious reason: everyone who reaches it was just refused a session, so a provider fetching
`/api/me` would only bounce them off the gate again. It is **not an error page** — the person
did everything right and is early — so it asks them for nothing and offers no next step to
chase. `PrivateBeta.test.tsx` pins that (no textbox, no button, no link); keep it, since the
page it replaced told people to go and find an admin.

**`tests/layout/other-shells.spec.ts` is what can see the nav actually lay out** — in the
gate, at 390px and 1440px: on one row, inside the viewport, clear of the brand, right of centre, and
**≥32px tall**. That last one is not padding: `.kiln-btn` has no `height`/`min-height` and
`line-height: 1`, so its entire height comes from `padding: 11px 20px` — writing the mobile
override as the `padding` shorthand zeroes the vertical half and collapses both buttons to a
16px sliver. Every DOM test still passed (the element is present and "visible"), and only the
browser caught it. Use `padding-inline` for any future per-viewport tightening here.

`GITHUB_SETUP_PATH` (the same route, `?setup=1`) is the **only** sanctioned second link, and
it is a screen request rather than a second grant: it goes straight to GitHub's chooser, for
the **already-connected** user who wants to change accounts or repositories. The plain route
cannot serve them — they have authorized already, so it completes silently and shows them
nothing. Use it for switch-account affordances only (`RepoField`, `Onboarding`'s connected
state); a signed-out or unconnected user always gets `GITHUB_CONNECT_PATH`.

`MeSettings.github_connection` carries `installation_id` and `configure_url` (GitHub's own
installation-settings page) instead of the `scopes` array it had under the OAuth App. Nothing
renders `configure_url` yet — the "Configure on GitHub" affordance belongs on the connect
step, which the App migration deliberately left untouched — but it is on the wire and ready.
`status: 'unknown'` now means "a credential is stored with no installation behind it" (a
hand-typed PAT or the bootstrap token), not "scopes unrecorded".

`ProjectFields` has **no free-text repo URL field**. The repo is picked from the connected
GitHub account (`RepoField` in `ConfigFields.tsx`, fed by `useGitHubRepos` →
`GET /api/github/repos`).

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

### First-run setup is a guided flow, not a form (`Onboarding.tsx`)

A signed-in user with `me.projects.length === 0` gets a **three-step flow**, one screen per
decision: **Connect GitHub → Choose your project → Choose your provider** (+ that provider's
key), then "Finish setup". It replaces the single crammed project form that used to live here.

- **The ordering is load-bearing, not cosmetic.** Step 2 can only list repos once step 1's
  credential exists; step 3 can only know *which* key to ask for once a provider is chosen.
  Don't reorder the steps or merge them back into one screen — that's the whole feature.
- **Step 1 usually arrives already satisfied.** Since the OAuth flows merged, signing in
  grants `repo`, so a new user hits step 1 in its *connected* reading — it confirms which
  account and how many repos before they pick one. Don't delete the step as redundant: the
  disconnected branch still catches accounts that predate the merge or revoked the grant,
  and it is where the `repo`-scope explanation lives.
- **It reuses settings' controls, it does not clone them.** The repo picker is `RepoField` and
  the key field is `SecretCredentialRow`, both exported from `ConfigFields.tsx` for exactly
  this. A second repo-picking or secret-entering implementation is how a free-text repo URL or
  a non-write-only secret drifts back in. `ProjectFields` is deliberately NOT used — the flow
  asks for a subset of its fields, and reshaping it would move DOM three other surfaces style.
- **Finish writes the key BEFORE the project** (`saveSettings` → `saveProject`), so a project
  never comes alive pointing at a provider whose credential hasn't landed. The key field
  auto-saves on blur *and* "Finish setup" commits it, so a `keyInFlight` ref guards the pair —
  clicking the button blurs the input, which would otherwise fire two saves for one key.
- **It never tracks "did I just finish."** A successful `saveProject` makes `me.projects`
  non-empty and `Dashboard` swaps the whole view for `Settings`; a failed one leaves it empty,
  so the user simply stays on the last step with `error` beneath it. Don't add local success state.
- **`PROVIDER_CREDENTIAL` maps a provider key → its credential slot** (`amika` →
  `amika_api_key`). The dashboard is otherwise data-driven about providers (choices render from
  `me.providers`, naming none), but the slots are provider-named *in the wire schema*, so the
  mapping has to exist somewhere. A provider with **no entry gets no key field at all** — which
  is what keeps the table from gating new providers, and what makes `mock` work keyless.
- **`me.providers` may be absent** (deployment didn't enable descriptors) → the provider step is
  **dropped entirely** and `agent_provider` is omitted, so the project keeps resolving to
  `AGENT_MODE`. Two steps, not an empty third one.
- **Onboarding needs its OWN `svg[data-icon]` sizing rule** — the settings one is scoped under
  `[data-role='settings']`. Without it every glyph renders at the SVG default 300×150 — with
  every DOM test still green, since the elements are all present at the wrong size. Measured in
  `tests/layout/other-shells.spec.ts`, along with the settings page's two-column geometry.
- The provider radios are wired `htmlFor`/`id` with the hint text **outside** the `<label>` —
  a wrapping label would absorb "Needs your Amika API key." into the radio's accessible name.
  Same trap as the credential rows' validity glyph.
- **E2e:** `keyless-onboarding.spec.ts` is the ONE spec that drives the flow end to end (it
  needs `KILN_GITHUB_MODE=mock` for step 1, which no headless test can complete against real
  GitHub). Every other spec seeds a project over `PUT /api/project` instead of walking the
  steps — don't couple new specs to the flow.

### `/signup` replays that flow on demand (the sign-up rehearsal)

`src/signup/` is a **second mount of the real flow, not a second flow.** It exists because
the team iterates on sign-up and `/dashboard` only shows the guided steps while
`me.projects` is empty — the second look used to cost a fresh or wiped GitHub account.
`Signup.tsx` mounts the very same `SignIn` + `Onboarding` over a simulated store
(`signup-run.ts`). If you change the flow, you change what `/signup` shows; that is the
whole design, so **never fork the components to make the rehearsal easier.**

- **Reads are live, writes go nowhere.** `GET /api/me` and `GET /api/github/repos` are the
  real calls (the tester's own login, repos and the deployment's real providers); every
  write lands in the simulated `Me`. This is not squeamishness: `saveProject` is
  `PUT /api/project`, an **upsert over the caller's FIRST project**
  (`identity.UpsertProject`), so a rehearsal that reached the network would rewrite the
  project the tester actually works in — and provider keys are user-scoped, so a throwaway
  key would replace the real one every project of theirs runs on. `Signup.test.tsx` asserts
  the whole flow finishes with every transport write un-called; keep that case.
- **Two paths, `?as=new` (default) and `?as=returning`.** They differ ONLY in the account
  `accountForPath` hands the flow — new blanks the settings and the GitHub connection,
  returning passes the real ones through. **Both drop `projects`**, which is what removes
  the "already onboarded" gate. The path rides on the query param so it is linkable, and
  the banner's switch writes it.
- **Restart is a remount, never a reset method.** `SignupBody` keys `SignupRunView` by
  `path + runId`; "Start over", the path switch and "Run it again" all bump `runId`. A
  reset routine would silently miss a field (the step index, a half-typed key, the
  simulated grant) the first time someone adds one.
- **GitHub's consent screen is the one thing faked**, because `/auth/github/callback`
  always redirects to `/dashboard` and a real grant mid-run would end the replay. The
  seams for it are two optional props, absent everywhere else in the app and changing no
  DOM when omitted: `Onboarding`'s `onConnect` (Connect / Switch account become buttons)
  and `SignIn`'s `onStart`. `Onboarding`'s `overrideGitHub` is a **transform** over the
  live reading, not the reading itself, so `useGitHubRepos` still fires exactly once, in
  `Onboarding`, whoever is watching.
- **The simulated store mirrors the real one's SEQUENCE, not just its results** — pending
  set → save → chained verify → pending cleared — because the credential indicator going
  `…` then `✓` is part of the experience being rehearsed. `credentialKeyIn` moved to
  `integrations-config.ts` so both stores ask that question the same way.
- Signed out for real, `/signup` renders the ordinary `SignIn` with the ordinary link: the
  allowlist check behind the OAuth route is what decides whether the tester gets in, and
  it cannot be simulated.
- **Styling: the rehearsal renders inside `[data-role='dashboard']`** so every
  onboarding/sign-in rule in `Dashboard.css` applies unchanged — `Signup.css` only styles
  what the route ADDS (the banner, the done panel) and is scoped under
  `[data-surface='signup']`. Watch shorthands there: `border: 0` on the
  link-turned-button `switch-github` weighs (0,2,1) against Dashboard.css's (0,2,0) and
  silently takes that control's underline with it — reset per side.

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
  `aria-hidden`, so accessible names come only from the visible text. **One exception, by
  request:** the project dialog's delete is the trash glyph alone, so it carries an explicit
  `aria-label` (plus `title`) and is sized in px rather than `em` — there is no neighbouring
  text for it to stay proportional to. Any other icon-only control needs the same two things.
- **Selects are drawn by us, not the platform** — `appearance: none` plus a chevron made of two
  `currentColor` `linear-gradient` stripes (`Dashboard.css`, mirrored in `ProjectsManager.css`).
  Gradients, not an SVG data URI, because a data URI can't read a token and would hard-code a
  hex in one theme. Consequences: any later rule touching a select must use `background-color`,
  never the `background` shorthand (it resets the chevron), and the `padding-right` that keeps a
  long option label out from under the glyph travels with it.
- **Layout is measured, not string-matched** (`tests/layout/other-shells.spec.ts`) — jsdom
  does no layout, so without it the page could silently revert to one mobile-style column and
  every DOM test would still pass.

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

## The ticket detail sheet's two direct writes

The sheet is read-only inspection over a read-only board (D5) as far as the ticket's *state*
goes — Accept / Delete / Poke all express intent that the caller routes **through the brain**.
Three things bypass it — the first two because they are the user's own input rather than a
transition, the third because it acts on the *sandbox* behind the ticket's slot rather than on
the board at all:

- **The sandbox toggle** (`onSetKeepSandbox` → `setTicketSandbox` → `POST
  /api/tickets/{id}/sandbox`): saving a ticket's sandbox stops the board recycling its worker,
  so an agent can keep working in the same workspace across turns. It is a *setting on the
  ticket*, so it writes directly — round-tripping a toggle through an LLM pass would be slow
  and non-deterministic for no gain. It reads "Save sandbox when done" and lives in the gear
  menu below, not in the sheet's body.
- **The text edit** (`onEditText` → `editTicketText` → `POST /api/tickets/{id}/text`): pressing
  the rendered body turns the title and body into a field and a textarea. This one skips
  the brain for the opposite reason — **an LLM pass is the thing being avoided.** Describing a
  wording change out loud and letting the brain rewrite the ticket is what drifts from what
  the user meant (that drift is the whole reason the affordance exists), so the typed text has
  to land verbatim. Never "improve" this by routing it back through the brain.
- **The sandbox overrides** (`onKillSandbox` / `onReassignSandbox` → `killTicketSandbox` /
  `reassignTicketSandbox` → `POST /api/tickets/{id}/sandbox/kill|reassign`): **Re-create
  sandbox** throws this ticket's workspace away and brings a fresh one up on the same slot,
  leaving the ticket where it is; **Move to free sandbox** rebinds it to a free slot and
  restarts the work there. They skip the brain for a third reason again — they exist so the
  user can reach *past* the orchestrator when a sandbox is wedged, and an override that waits
  on an LLM turn is not an override. Both are scoped to `SANDBOX_CONTROL_STATES`
  (working|blocked, mirroring the board's own "a worker is bound" precondition) and each is
  gated behind a `window.confirm` naming what is lost — the same gate the blocked-ticket
  Delete uses. The status line and Move's *presence* come from the board snapshot the sheet
  already has — `agents[].status` for the sandbox's real session state
  (`SANDBOX_STATUS_LABELS`), `worker_free > 0` for `canReassign` — so the user is never walked
  into the server's 409.

All three share the two consequences below. The text edit adds three of its own:

- **Gated on `EDITABLE_STATES` (shaping/ready), mirroring the board's `shape_ticket`
  precondition** — not a client-side opinion. Widen the two together or the body invites an
  edit the server answers with 409.
- **The body IS the affordance — there is no pencil** (removed; don't reintroduce one). The
  rendered Markdown of an editable ticket is wrapped in `[data-role='detail-body-edit-target']`,
  and pressing it swaps that same region for the textarea, so the words never move to be
  changed. Four things make that safe and reachable, all covered by tests:
  - **A plain `div`, never a `<button>`/`role="button"`.** A button announces its contents as
    its own label instead of as the document they are — and the body is what the sheet exists
    to show — plus Markdown can contain links, which cannot live inside a button.
  - **The keyboard/AT route is a separate control**, `[data-role='detail-body-edit-key']`
    ("Edit description"), clipped off-screen until focused (skip-link shape). It is the *only*
    way in without a pointer, so it must stay tabbable — clipped, not `display: none`.
  - **Three presses inside the body must NOT open the editor**: a click that ends a *drag*
    (the body sits inside the sheet's scroll region, so most fingers on it are scrolling — and
    a drag at the sheet itself still delivers its click), a click on an `<a>` (following a
    reference isn't a request to rewrite the sentence) and a click that ends a text selection
    (otherwise copying a ticket is impossible). All three guarded in `editFromBody`.
  - **The pressed wash answers a press only, never a scroll** — see the emulated-hover gotcha
    below. `:hover` is gated behind `@media (hover: hover)` so touch can't inherit it, and
    touch gets `[data-pressed]` instead, set by the component's own pointer handlers a beat
    (`PRESS_FEEDBACK_DELAY_MS`) after a finger comes to rest and dropped the moment it travels
    past `TAP_SLOP_PX` (or the browser cancels the pointer, which is how a touch scroll ends).
  - **An empty body renders a placeholder** ("Add a description"), because an editable ticket
    with nothing written would otherwise have nothing to press. Entering the mode focuses the
    *body* textarea with the caret at the end — the body is what the user pressed.
- **The `<Drawer.Title>` stays mounted while editing**, visually hidden by a *clip* rule
  (`[data-editing='true']`), because Radix names the dialog by it. `display: none` is the one
  hiding style an accessible-name computation may skip — don't "simplify" it to that.
  Measured in `tests/layout/ticket-detail.spec.ts` (jsdom does no layout, so nothing else in
  the gate would catch a duplicated on-screen title).
- **Save sends only the fields that changed**, so editing the title can't clobber a body the
  brain rewrote while the sheet was open; an unchanged draft sends nothing at all. Edit mode
  also replaces the dock's state actions (Accept/Delete/Poke/mic) wholesale with Cancel/Save.

Two consequences worth keeping when you touch either:

- **None of them closes the sheet.** Every other action closes on tap; these are things done
  *while reading* (a setting flipped, wording corrected — and after a correction the user
  should be looking at the corrected ticket; after a kill they want to watch what happens to
  the sandbox), so `PrimaryScreenView` passes them straight through without `closeTicket()`.
- **Both are optimistically shown, time-boxed.** The value lives on the board snapshot, which
  only comes back over the stream, so the sheet renders `pendingKeep ?? ticket.keep_sandbox`
  (and `pendingText` over `ticket.title`/`.body`) and drops the overlay as soon as the
  snapshot agrees — or after `SANDBOX_OPTIMISTIC_MS` / `TEXT_OPTIMISTIC_MS` if the write never
  lands, the same self-healing shape the feed store's optimistic card hides use. `PrimaryScreen`
  also refreshes the board on a failed write so it snaps back at once instead of on the
  time-box (for the edit, that covers the 409 a ticket that left the backlog mid-sheet returns).

### All of it lives behind ONE gear (`TicketDetailSandboxMenu`)

The three sandbox affordances used to be a checkbox, two buttons and three paragraphs of
explanation at the foot of the sheet's scrolling body. They are now one gear at the **end** of
the sheet's **status row** (under the title, on the sheet's right edge, after the
"In progress"/"Blocked"/"Done" badge) opening a dropdown. Points worth keeping:

- **Each item self-gates on its callback arriving.** `TicketDetail` decides — toggle whenever
  `onSetKeepSandbox` is wired, the two overrides only when `hasSandbox`, Move only when
  `canReassign` too — and the menu renders what it is handed. No gear at all when nothing is
  wired, so a read-only sheet is byte-identical.
- **`canReassign: false` now HIDES Move rather than disabling it.** Inside a menu a dead line
  is pure noise; there is no room for the "why it's greyed out" hint that justified the
  disabled button.
- **Escape must reach the menu before the sheet, and that takes `window` capture.** Radix
  (under vaul) listens for Escape *in the capture phase on `document`* and mounts first, so a
  `document` listener in either phase — capture included — loses the race and the whole sheet
  dismisses. The menu listens on **`window`** with `capture: true` (the first node in the
  propagation path) and calls `stopPropagation`, only while open. Any future popover inside
  this sheet needs the same trick.
- **The panel is absolutely positioned inside the header, which carries `position: relative;
  z-index: 1`** so it paints over the scrolling body rather than under it. It opens
  down-and-**left** (`right: 0`, `transform-origin: top right`) from the trigger at the row's
  **end** (`margin-left: auto` on the menu), which keeps it inside the sheet's `overflow:
  hidden` in both shells — the desktop panel needs no re-anchoring (unlike the bell, above).
  **The anchor tracks the trigger:** it hung `left: 0` while the gear led the row,
  and moving the gear without moving the anchor would clip the panel off the sheet's edge.
  `tests/layout/ticket-detail.spec.ts` measures exactly that: the gear on the sheet's right
  edge, and its panel opening back INTO the sheet rather than off the edge.
- **A closed panel stays mounted** (so it animates both ways) and is taken out of the page by
  `aria-hidden`. Consequence for tests: a closed menu's items are *absent* from role queries,
  so every test opens the gear first.

### The sheet's footer swaps its RIGHT-HAND controls; nothing on the row ever moves

The footer is left-group / right-group in both readings: the voice cluster
(`[data-role='ticket-detail-voice-actions']`) at the bottom-left, the trailing slot at the
right. What changes when a voice session goes live is only **who holds the trailing slot** —
the state actions (Poke, Delete, Accept) withdraw *as a group* and the cluster's own Send and
× take their place. **The mic does not move, at all, ever.** Both shells, one markup.

- **This is the fix for the original arrangement, so don't reintroduce it.** The cluster used
  to *travel* to the trailing end on `data-position="lead"|"trail"`, which meant the mic slid
  out from under the finger that had just tapped it and shoved Poke aside to make room. A
  control that jumps when you activate it is the whole bug; "elements stay put" is the rule
  that replaced it.
- **The cluster spans the row and distributes its own children** — `flex: 1` +
  `justify-content: space-between`. That one rule does all of it: the mic is the first child,
  so it sits at the row's left edge in *every* reading; with the mic alone inside,
  `space-between` degenerates to flex-start and the cluster's growth is what pushes the state
  actions right (exactly what the old `margin-right: auto` did). No `order`, no margin flip,
  no position attribute — if any of those come back, the mic has started travelling again.
  Measured in Chromium when it landed: 0.00px of mic movement, and Send's right edge on
  Accept's to the pixel.
- **Send and × are ONE box** (`[data-role='ticket-detail-voice-send']`), rendered by
  `TicketDetailVoiceActions` as the cluster's second child. Grouping is load-bearing:
  `space-between` over three loose children would strand the × in the middle of the row.
- **All three state actions share one gate, and that is deliberate.** The trailing slot is one
  slot — leaving any of them up would just mean Send and × arriving *beside* them and shoving
  them along, which is the shuffling this exists to remove. So a *blocked* ticket's Poke and
  Delete now stand down too (they used to be exempt). Each also earns it alone: Accept's slot
  is Send's, Delete is a destructive control appearing the instant someone opens their mouth,
  and Poke offers to nudge an agent while the user composes the message that would say it
  better.
- **`MicButton` is never re-parented and never re-ordered**, which is what keeps it from being
  unmounted and remounted mid-utterance (that would take its `setTicketContext` registration
  and its volume-glow rAF loop with it). It is rendered unconditionally as the cluster's first
  child. Held by a DOM assertion that the cluster element is *identical* across the flip
  (`TicketDetail.test.tsx`) — the arrangement itself is CSS, so if the mic starts travelling
  again the assertion to add is a geometry one in `tests/layout/ticket-detail.spec.ts`.
- **`TicketDetail` still knows nothing about the voice store**, and that is what shapes the
  seam. `TicketDetailVoiceActions` (a `useVoice()` consumer, like `TicketDetailTranscript`)
  reports `onActiveChange(boolean)` up to `PrimaryScreenView` / `DesktopScreenView`, which
  hold it in `useState` and hand it back as the `voiceActive` prop. It is a **boolean on
  purpose**: it fires twice an utterance rather than once a word, so neither screen
  re-renders on transcript churn. Wire it in **both** shells — the state is per-shell and
  forgetting one is the obvious way to ship this half-done.
- **A session is live when the mic is listening OR there are words on screen**, not just the
  latter: an end-of-turn final pauses the mic while the utterance sits armed in the grace
  window, and the footer must not snap back to Accept underneath a transcript the user is
  still deciding about. Send is rendered-but-`disabled` before there is anything to send, so
  the × beside it doesn't shuffle sideways when the first partial lands.
- **The sheet's × is not the dock's ×.** The dock's discards the transcript and deliberately
  leaves the mic listening (`cancel` alone). The sheet's is the way *out* — `cancel()` **and**
  `pause()` — because that is what returns the footer to Accept, and a sheet has no keyboard
  toggle or second controls row to escape through. Don't "unify" them by changing `cancel`.
- **`MicButton` is the orb and nothing else now.** Its old `sendable` mode (orb gives way to
  send + clear) is gone: the sheet keeps the orb up while speaking because the glow is the
  only thing reporting the mic is live. Every placement renders its own send/discard around it.
- **A view test mocking `useVoice` must rest at `micState: 'paused'`.** That is
  `initialVoiceState()` — the app never opens listening — and it is now load-bearing rather
  than cosmetic: a `'listening'` mock puts *every* sheet in that file into the speaking
  arrangement and Accept vanishes from tests that never mentioned voice.
  `PrimaryScreenView.test.tsx` had exactly that wrong and only the swap exposed it.

### Every footer glyph is the mic's disc, Poke included

`[data-role='detail-accept']`, `[data-role='detail-delete']` and `[data-role='detail-poke']`
share ONE rule in `TicketDetail.css`, whose numbers are copied from `[data-role='dock-mic']`
in `PrimaryScreen.css`: 54px, round, card fill inside a strong outline, raised shadow that
collapses under the thumb. What distinguishes them is the glyph alone — the check in accent,
the trash in danger, the mic's bars muted, and Poke's 👉 which brings its own colour (so its
own rule states nothing but the 22px the two stroked glyphs are drawn at). None of the three
carries a `:hover` or a `[data-surface='primary']` skin, and both absences are asserted:
a touch device emulates hover, and a primary-surface override at higher specificity is exactly
how the row drifts apart on the one surface the app renders. Poke was the last holdout — a
2.1rem outlined circle beside three 54px discs, plus a `13px` ghost-pill skin with a `:hover`
— and it read as a different *kind* of control rather than a quieter one.
The mic is dressed in the *other* stylesheet (`PrimaryScreen.css`, unscoped, so the sheet's
footer picks it up wherever it is placed), which is exactly the kind of seam an edit to one
side walks past. If you restyle the mic, restyle these with it.

### Poke is offered on every working ticket, idle session or not

Poke shows on **working|blocked whenever `onPoke` is wired** — there is no `agentIdle` prop
any more. It used to be gated on the board snapshot's `agents[].status === 'idle'`, which hid
the button for most of an in-progress ticket's life: `building` is the normal reading while an
agent is mid-turn, and that is precisely when a user watching it go the wrong way reaches for
the nudge. The session status is too coarse to mean "needs nothing" — and poking costs nothing
either way, since the client only posts an intent and the brain decides whether to
`send_to_agent`. `openAgentStatus` still exists in all three views that mount the sheet
(`PrimaryScreenView`, `DesktopScreenView`, `KanbanScreenView`), but now feeds only the gear
menu's `sandboxStatus` line. Don't reintroduce a liveness gate here.

## A feed card's body is Markdown — except the two that aren't

`FeedCardBody` takes a `markdown` flag, and the three bodies on a card disagree on purpose:

- **The brain-authored body (update/blocker/preview) renders as Markdown** (react-markdown +
  remark-gfm, the same pair `TicketDetail` uses). The brain writes in Markdown — the 06 prompt
  tells it to — so a ticket summary that leads with `## What changed` used to read as literal
  syntax in the feed.
- **The done card's work summary stays verbatim text.** It is a commit message / PR
  description, not Markdown: a renderer would fold its hard-wrapped lines into one run and eat
  a leading `#`. Its `white-space: pre-line` rule is what keeps the line breaks.
- **The proposal digest stays text too**, because it lives inside the `feed-card-open`
  **button** that opens the ticket — block elements and nested links cannot go there (the same
  reason the ticket sheet's body is a `div` and not a button). The sheet one tap away renders
  the same body as Markdown, so nothing is lost. If a ticket ever wants the digest formatted,
  that means restructuring the click-through, not just flipping the flag.

Consequences worth carrying:

- **The body element is a `div`, not a `p`** (block children can't live in a paragraph). Every
  rule that dresses it keys off `data-role`, never the tag — keep it that way.
- **The clamp still works over block children** — `-webkit-box` + `-webkit-line-clamp` counts
  line boxes across them — but jsdom can't see it. It was verified in a browser (three lines
  shown, cue in place, expand-in-place intact); re-verify the same way if the dressing changes.
- **Markdown blocks carry a TOP margin only, and the first child's is zeroed** — stated as one
  rule per element rather than a `* + *` owl, because `[data-role='feed-card-body'] p` (0,1,1)
  silently beats `[data-role='feed-card-body'] > * + *` (0,1,0) and the spacing vanishes. The
  first-child reset needs a `:not()` to climb past them. Headings are body-size and bold: a
  card whose premise is a scannable three-line preview must not double in height for a `##`.
- **A link in the body is still a link.** The clamped body is the expand toggle, so its click
  handler ignores presses that land inside an `<a>` (`closest('a')`, same guard as the sheet).

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

## Seen cards collapse away, one "Show earlier" brings them back (feed, 08 D2‴)

The default feed shows **only what the user hasn't caught up on**: everything already
seen when the visit began is collapsed out of it, with no timer — the old
`SEEN_LINGER_MS` 10-minute window is gone, and so is the "Show seen notifications"
toggle. There is exactly **one** foot-of-feed control, `[data-role='feed-show-earlier']`,
always reading **"Show earlier"** in both shells. It reveals the collapsed cards, then
keeps paging older history under the same label; it has no counterpart that puts them
back (the reveal is view state and resets on reopen). Visibility only — nothing is
retracted, so this is unrelated to swipe-dismiss above.

- **The boundary is the seen FLOOR, an id, not a per-card clock.** `seenFloorRef` holds
  the server's last-seen high-water; `collapsedSeenIds` collapses every accumulated id
  at or below it. `seen_at` is still on the wire (and still what the server stamps) but
  the client no longer reads it. **Unseen ⇒ above the floor ⇒ never collapsed**, at any
  age: the floor only ever advances to a mark the server stamped seen, and that
  invariant is the whole safety property. Never key a filter here on `created_at`.
- **The floor is per VISIT, and that is the load-bearing part.** Rendering a card acks
  it seen within a round-trip, so collapsing on the raw ack would erase a notification
  a second after the user opened the app to read it. The floor is therefore read at the
  **first snapshot of a visit** (in `applySnapshot`'s freeze block, deliberately
  *before* `ackVisibleSeen` advances the server's mark) and again on
  **visibilitychange → visible** (from our own `ackedRef`, so it costs no fetch).
  Nothing moves while the user is looking; what they read is gone when they come back.
  That return-to-visible advance is also the only thing that declutters a surface which
  is never reloaded — the desktop window left open all day.
- **A project switch is NOT a new visit.** `seenFloor` rides in `CachedFeed` and is
  restored verbatim, for the same reason the optimistic suppressions are: a switch back
  paints what was last on screen (12 §4.1). Recomputing it from the server's
  ack-advanced mark would blank a feed the user was reading a moment ago.
- **Everything merges through `remerge()`.** The store has three stacked suppressions
  (optimistic ticket hides, swipe dismissals, collapsed seen cards); they used to be
  re-applied by five copy-pasted `setFeed(mergeFeed(...))` blocks. Add a new one inside
  `mergeFeed` + `remerge`, not at a call site, or the suppressions drift apart depending
  on which mutation path fired last.
- **`showEarlierRef` is a ref with no state beside it.** Every merge callback reads it,
  so `applySnapshot` stays render-stable; make a merge callback depend on a `showEarlier`
  *state* and the `subscribeStream` effect below it re-subscribes — revealing would drop
  and reopen the SSE connection. Flipping it is always followed by `remerge()`, which is
  what re-renders.
- **`showEarlier()` is one action with two jobs, in order:** reveal the collapsed cards
  if any, else page older history (`loadMoreHistory`, which force-reveals for the same
  reason — an older page is almost entirely long-seen updates and would otherwise fetch
  straight into hiding). `hasEarlier` (collapsed count OR `hasMoreHistory`) is the gate,
  so the button is never on screen with nothing to do — that, not a hardcoded "always
  render", is what "one always-present control" means in practice.
- **The control renders outside the empty/non-empty branch** in BOTH shells. Collapsing
  the last card leaves the "all clear" / "All quiet." resting state — the one place the
  user most needs the way back — so keep it out of the `isEmpty` ternary, and out of the
  desktop `<ol>` (which isn't rendered at all when there are no cards).
- **It is pinned to the FOOT of the feed region, directly above the input, in EVERY
  state** — both shells, one mechanism, no matter the card count or the scroll offset.
  This used to hold only when the feed was empty (the resting block is `flex: 1` /
  `flex: 1 0 auto` and incidentally carried its next sibling down), and with any cards at
  all the control simply ended the backlog: mid-region on a short feed, off the bottom of
  a long one. Two declarations on the unscoped rule in `PrimaryScreen.css` do it, and
  BOTH are needed — `margin-top: auto` for cards that fall short of the scrollport, and
  `position: sticky` for cards that overflow it. The desktop rule restates only
  `margin: auto auto 0` (three values, so the horizontal `auto` that holds it to the
  cards' 720px measure survives) and deliberately restates neither `position` nor
  `bottom`.
- **The sticky offset is `0`, and adding the overlay vars to it is the trap.** A sticky
  box is clamped to its containing block, whose bottom edge is the feed's CONTENT box —
  which the region's `padding-bottom` already holds clear of the transcript and the toast
  band. Naming `--dock-overlay-height` / `--feed-bottom-inset` here applies that clearance
  a second time: a sliver of card stranded under the control at rest, and — far worse —
  the control floating a whole transcript's height off the dock mid-utterance. If the
  reserve needs changing, change the padding. (This is the one place the "anchor to the
  dynamic height of the layers below you" principle is satisfied *indirectly*.) The band
  half of that clearance is then handed back by a `transform` — see the toast-overlay
  bullet below — which is not a contradiction: `bottom` moves the sticky box and takes
  the reserve with it, a transform moves only the paint.
- **The control needs an opaque fill AND an opaque skirt below it** (`background` +
  `box-shadow: 0 var(--space-5) 0 var(--space-5) var(--surface-page)`). Cards scroll under
  it now, and the reserve beneath it is still scrollable area they pass through — without
  the skirt the control ends the feed with a line of the next card stranded under it. It
  needs no measuring: the feed's own `overflow-y: auto` clips it to the region's bottom edge.
  **Offset and spread, never offset alone.** An outer shadow is the border box moved by the
  offset and then clipped back *out* of that box, so the original `0 var(--space-10) 0`
  started 2px below a 38px-tall control — a 2px hairline of scrolled card sitting directly
  on the dock's separator, which is the one thing the skirt is for. Half offset + half
  spread reaches the same 40px with its top edge on the control's own at any height.
- **The control carries NO `z-index`, and `[data-role='dock-region']` carries `z-index: 1`.**
  This pair is the feed-vs-dock layering and it is easy to get backwards. The band and the
  transcript stand *inside* the reserve the skirt paints, so a `z-index` on the control
  lifts the skirt over them — a grey blob across the band's separator and the top of every
  toast, and over the transcript's hairline mid-utterance. Nor can the dock answer from its
  own side: its keyboard-lift `transform` makes it a stacking context, sealing the band's
  `z-index: 6` inside, so only a number on the REGION speaks for the layer. The control
  needs no lift of its own — a positioned box already paints above the in-flow cards.
- **A toast OVERLAYS the control; it does not push it up.** The reserve still grows with
  the band (the cards need it), but the control gives that growth back in paint:
  `:has([data-role='toast-stack'])` on **either shell root** sets `--show-earlier-drop`,
  a term in the control's one `transform`, so it holds its place — 20px off the dock on
  the phone, 12px off the composer region on the desk, both measured — and the band,
  opaque and full-width, covers it. One rule for both roots, in `PrimaryScreen.css`: the
  publisher, the band and the control are the same objects in both shells, and a second
  copy in `DesktopScreen.css` is how the two would drift apart. Three things about that
  rule, each of which was a bug on the way in:
  - **The drop is `--feed-bottom-inset` MINUS `--activity-rest-gap`**, not the whole
    inset. An empty activity row is not 0px tall — it is that 12px gap (the same
    declaration that floats the thinking chip off the dock, which is why it is a var
    read by both) — and that much is already under the control at rest. Give it back
    too and the control settles 12px *lower* under a toast: the same bug, mirrored.
  - **It is gated on a real toast stack.** The reserve's other occupant is the thinking
    chip: narrow, centred, floating, no fill to hide anything behind it. Dropping
    through that lands the chip on the control's label. Thinking alone must keep its
    lift; thinking *with* toasts is safe, because the chip renders above the band's top.
  - **One `transform`, composed from vars** (`--show-earlier-drop` + `--show-earlier-press`).
    A `:active { transform: translateY(1px) }` of its own would overwrite the drop and
    snap the control up out of the band at the moment of the tap.
  The trade this accepts: while a toast is up the control is covered, and the band takes
  its own pointer events, so it is briefly untappable. That is what "overlay, don't push"
  asks for; toasts clear themselves in seconds. The margin is thin — the shortest band
  the app can show (one board toast, no `say`) is 63px against a control whose top edge
  lands 58px off the dock — so **re-measure with the repro script if this control's type
  or padding grows**, or its rounded top edge will show over the band's separator.
- **`[data-role='desktop-feed']` is a flex column in every state, with no STATIC bottom
  pad and one dynamic term.** Both were previously gated on a `data-empty` attribute,
  which is gone — nothing read it once the anchoring stopped being conditional. Its old
  `--space-10` bottom pad was reading air for the last card; that moved to
  `[data-role='desktop-feed-list']`'s `padding-bottom`, where it neither pushes the pinned
  control off the foot nor vanishes on a feed with nothing earlier to show. A *fixed* pad
  on the region would only sit under the control and show cards through it. What the
  region does carry is `padding-bottom: var(--feed-bottom-inset, 0px)` — the band's live
  height, so the desk can scroll its last card clear of the toasts the same way the phone
  does. That reserve is for the cards; the control cancels it back out of its own painted
  position (see the toast-overlay bullet above), so a desk toast covers it rather than
  moving it.
- **jsdom sees none of this, so it is measured instead.** `tests/layout/` renders both
  shells and asserts the geometry directly: the control's standoff from the dock is the
  same number with a band up as at rest (the "toasts overlay, never push" claim, in both
  shells), the band is what hit-tests over the control — top edge included — and the
  control still ends the feed at every card count and on a viewport too short to spare the
  room. The DOM half — the control being the LAST child of `[data-role='backlog']` / the
  feed region, which is the containing block the anchoring hangs off — stays in the two
  view tests. Add to `tests/layout/bottom-stack.spec.ts` when you touch this.

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

- **A touch device *emulates* hover, so `:hover` is not a pointer-only rule.** A finger that
  merely starts a scroll on an element with a `:hover` background paints it on the way past —
  the element reads as pressed when nothing was pressed (the bug behind the ticket body's
  wash). Any press/hover feedback on something inside a scroll region belongs behind
  `@media (hover: hover)`, with touch's half driven from the component instead. Two rules
  for that half, both learned the hard way: it cannot go on at `pointerdown` (a scroll starts
  with one too — wait a beat first), and it must come off on `pointercancel`, which is how the
  browser ends a touch that turned into a scroll. `TicketDetail.tsx`'s
  `beginPress`/`trackPress`/`abandonPress` is the worked example; jsdom matches no media
  queries, so the hover half of the wash is not asserted at all; the press half is DOM and
  is.

- **Sending pointer events into the vaul sheet hits two jsdom gaps in vaul itself**, which
  surface as uncaught `TypeError`s that fail the run even when every test passes (vitest exits
  1 on unhandled errors). The events reach vaul's own drag handlers on `Drawer.Content` above
  whatever you aimed at — as they do in the browser, where dragging the sheet by its body is a
  dismiss path — and it then (1) calls `setPointerCapture`, which jsdom does not implement, and
  (2) reads `style.transform || style.webkitTransform || style.mozTransform`, where jsdom
  answers the first two with `''` and has no `mozTransform` at all, so the chain lands on
  `undefined` and vaul calls `.match` on it. Stub all three plus `mozTransform` in the test
  file — `TicketDetail.edit.test.tsx` does — rather than making the component swallow events
  to stay testable.

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
  - **The header carries no × — don't add one back.** Those three paths are the whole of
    dismissal, and a button was a fourth. It cost a chrome column down the sheet's right
    edge that the title had to be shrunk to clear; the header is now a single full-width
    heading column (title over status row) and the title reads at 24px on the primary skin.
    Both shells, one markup. Tests that used to close the sheet by clicking `Close` press
    Escape instead (`PrimaryScreenView.test.tsx`).

- **A `max-height` flex column shrinks EVERY item, not just the scrolling one.** The
  ticket sheet caps at `85dvh` and clips; flexbox removes the overflow in proportion to
  (shrink factor × flex base size), so with the default factor the dock lost height to a
  long ticket body and the live transcript inside it clipped its own text behind an
  invisible scroll (touch draws no scrollbar) — the "transcript is cut off" bug. Fix is
  shrink *priority*, not a bigger cap: the scrolling body carries an outsized
  `flex-shrink` (`flex: 1 100 auto`) so it yields first, while the dock keeps
  `flex-shrink: 1` + `min-height: 0` as a last-resort valve so its controls can't be
  clipped off a very short viewport. Same trap applies to any other capped sheet.
  Measured in `tests/layout/ticket-detail.spec.ts`, on a ticket long enough to cap the
  sheet: the dock renders at its intrinsic height and the body is the region that yields.

- **Assert layout as geometry, in a browser — never as the text of the stylesheet.**
  `tests/layout/` (in `make check`) renders the real client with every `/api` call stubbed
  and measures boxes and paint order. The repo used to slice rule bodies out of stylesheets
  with `?raw` and match them as strings; a string test passes when the rule is present and
  the layout is still broken, which is exactly how the same "Show earlier"/toast overlap
  shipped five times. The one thing still asserted about CSS *text* is what a sheet may not
  CONTAIN — no colour literals, no second theme switch, no literal z-index where the layer
  scale has a rung (`styles/stylesheet-discipline.test.ts`) — because you cannot observe
  the absence of a colour in a rendering. Anything positional goes in `tests/layout/`.

- **Viewport units: match the unit the container already uses.** On mobile Safari `vh` is
  the *large* viewport (browser chrome hidden), so a `45vh` child inside an `85dvh` parent
  can claim more than it looks like it does.

_(Accumulate: non-obvious traps and edge cases.)_

## The desktop shell (spec 13) — shells over four shared layers

`/app` renders **one of two presentational shells** depending on viewport width, and
`PrimaryScreen.tsx` is the switch. `/kanban` is a third shell over the same machinery. That
is 13 §13 Q4's answer, now settled as **D10**: one responsive tree, and the shells share
**stores, intents, the feed reading model and overlay behaviour — differing only in DOM
shape and CSS.** The rejected alternative was one tree restyled by media queries; the two
layouts don't share a DOM (a header/feed/dock column with a bottom-anchored overlay stack
vs. a rail beside a feed), and forcing one to be both is how the mobile screen's layering
gets broken by a desk rule.

**Sharing only the stores was tried, and it drifted.** That is what shipped first, and it
left 187 hand-copied lines between the two feed shells plus a third partial copy in
`/kanban` — and the same conceptual layout bug fixed five times, in alternating shells. Four
layers now sit under the shells; work belongs in the lowest one that fits:

| | Module | What it owns |
|---|---|---|
| **L0** taxonomy | `components/feed-kinds.ts` → `FEED_KIND_TRAITS`, `matchKind()` | What a card KIND is and means — the one place the six kinds are enumerated |
| **L1** intents | `components/ticket-intents.ts` → `useTicketActions()` | What the client may DO to a ticket, and how a failed write recovers |
| **L2** reading model | `components/feed-model.ts` → `readFeed()` | What there IS to show — membership, order, divider position, seen-ness, dismissability |
| **L3** behaviour | `components/use-ticket-overlay.ts` + `TicketDetailHost.tsx` | What a shell REMEMBERS — the open ticket, the deep link, the voice-active flag, the sheet's wiring |
| **L4** shells | `PrimaryScreenView` / `desktop/DesktopScreenView` / `desktop/KanbanScreenView` | What it LOOKS like. Markup, CSS, platform-only affordances |

**The rule: a shell file contains no `function` declaration and no `useState` above its
component body.** If it computes what to show → L2. If it remembers something → L3. If it
writes to the server → L1. Two things enforce it, because a documented convention is exactly
what these files already had:

- **An eslint rule** (`no-restricted-syntax`, scoped to the three shell files in
  `eslint.config.js`) banning program-level function declarations and `useState`/`useReducer`.
  It fires on the *fourth* shell before anyone reviews it. When purity pushes back — it did
  once, on the kanban card's accessible-name formatter — the answer is to give that view its
  **own** model module (`desktop/kanban-board.ts`), never to push presentation copy into a
  module the other shells share.
- **`feed-shell-conformance.test.tsx`** — a `describe.each` over shell adapters
  (`{ render, rowSelector, seenSelector, scrollSelector }`) asserting each *shared* rule once
  against both DOMs. A new shell joins by adding a row to `SHELLS`. It does not replace the
  per-shell suites, which still own what is genuinely one shell's (the phone's swipe,
  pull-to-refresh and bulk clear; the desk's loading line, roving focus and withheld resting
  state).

**Never write `card.kind === '…'`. Ask `feed-kinds.ts` (L0, D9).** The card-kind taxonomy —
`update`/`preview`/`poke`/`done`/`blocker`/`proposal` — is stated ONCE, as a matrix
(`FEED_KIND_TRAITS`) with a row per kind and a column per decision the app makes about a
card: where it comes from, whether Kiln authored it, whether it has a body, where a tap opens
the ticket, whether it offers Accept, whether it wears a tag, an image, or the landed-work
fields. Named predicates (`isBoardCard`, `isNotificationCard`, `isAuthoredUpdate`,
`rendersBody`, `opensDetailFromBody`/`FromHead`, `isAcceptable`, `showsKindTag`,
`carriesPreviewImage`, `carriesLandedWork`) read columns out of it; nothing else compares a
kind to a string.

- **It was eight files before this.** The wire guard in `transport.ts`, the store's filters,
  `cardTag`'s `switch`, the two `feed-model` predicates, and *seven* separate reads inside
  `FeedCardItem`. Because the union permits every kind everywhere, adding a seventh kind
  type-checked past all of them and then silently did nothing at each missed site: no tag, no
  body, no tap target, dropped on the floor by the transport guard.
- **The compile error is the feature.** A `Record<FeedCardKind, …>` is missing a property the
  moment the wire union grows, so a new kind breaks `feed-kinds.ts` with every unanswered
  decision listed. Verified by adding a seventh kind and reading the errors: the matrix, the
  tag copy in `feed-format.ts`, and `FeedCardItem`'s head mark — the three places a decision
  genuinely lives — and nowhere else, because everything else routes through the matrix.
- **`matchKind(kind, arms)` is the exhaustive switch**, for a decision that is one view's own:
  the head glyph (dot/👉/✅) and the tag words stay where they're rendered, and take their
  exhaustiveness from the helper. **Never a `switch` with a `default`** — `cardTag`'s default
  answered "Update" for anything unlisted, which is a plausible wrong answer instead of a
  build failure. And never a per-view copy of a fact the taxonomy should hold.
- **`transport.ts` imports `isFeedCardKind` from here**, deliberately: the kinds the wire may
  carry and the kinds every screen has decided about are the same set. The type edge runs back
  the other way (`FeedCard`) but is erased at compile time, so there is no runtime cycle.
- **The tests pin membership as explicit lists**, not spot-checks (`feed-kinds.test.ts`) —
  the failure mode is a kind quietly changing sides, which representative examples pass
  straight through — plus the matrix's internal invariants (a body-less kind has no body
  click-through; board state is never an authored notice).

**Two card taxonomies, and they must never be merged.** `notificationId()` covers all four
notification-backed kinds (update/preview/poke/done) — the store's accumulation, the history
cursor, the swipe's retract id. `authoredUpdateId()` covers only update/preview — the
last-seen divider and the seen de-emphasis, which are claims about what Kiln *said*. Both
were once called `updateId`, in three places. Merging them by name type-checks and silently
slides the "Earlier" divider onto the mechanical poke and done cards; `feed-model.ts`'s
header explains it at length and `feed-model.test.ts` opens with the cases that fail if the
two sets ever agree. The two SETS are now two columns of the matrix above (`source` and
`authoredNotice`); the two ID readers stay in `feed-model.ts`, because they are about the
nullable `notification_id` field as much as about the kind.

**What shared code must NOT decide.** `loading` is desktop-only — it is not in `FeedReading`
at all, so no shared code can start rendering a line on the phone. `isEmpty` is a *fact*: the
desk withholds "All quiet." while loading (that line asserts we asked and there was nothing),
the phone always renders its all-clear block, and that gating stays in each shell. The
divider's *index* is shared, its wrapper element is not. And "Show earlier" must stay last
**inside each shell's own scroll wrapper** — its `position: sticky` hangs off that containing
block, which is the bug that was fixed five times; the conformance suite pins the structure
and `tests/layout/` measures the geometry.

- **`useIsDesktop()` (`desktop/use-desktop-layout.ts`) is the only breakpoint.** The CSS
  deliberately carries **no** `min-width` media query for the shell — a second threshold
  could silently disagree with the JS one. Asserted in `styles/stylesheet-discipline.test.ts`.
  `useState<boolean>(false)` is explicit on purpose: `useState(false)` infers the literal
  `false`, and then every `if (isDesktop)` downstream trips `no-unnecessary-condition`.
- **`DesktopScreen.css` layers on top of `PrimaryScreen.css`** (the shell imports both, in
  that order). The feed card, divider, "show earlier" button, mic, and send/clear are all
  styled by *unscoped* rules in the mobile sheet, so the desktop shell inherits one visual
  language for free and only states what a desk earns. Don't scope those mobile rules under
  `[data-role='primary-screen']` — the desktop shell depends on them being global.
- **The accent budget is spent exactly once**, on the rail's `needs-you` dot. A test asserts
  `DesktopScreen.css` contains exactly ONE `var(--accent*)` rule and that its selector is the
  needs-you one. This is why the desktop send button is neutral where the dock's is accent:
  a window left open all day must not carry a permanently lit accent in the corner.
- **The desktop shell pins no theme — it follows the OS like every other route** (13 D6a).
  It used to stamp `data-theme="dark"` on `<body>`, which beat the system preference
  `ThemeColorSync` writes to `<html>` and gave one user paper on their phone and near-black
  at their desk. There is exactly one theme mechanism in this app: `ThemeColorSync` →
  `data-theme` on `<html>` → the semantic tokens in `tokens.css`, live on
  `prefers-color-scheme` flips. Don't reintroduce a per-surface override.
- **Every desktop rule therefore has to hold in both registers**, and a test asserts
  `DesktopScreen.css` names no theme (no `[data-theme=…]`, no `prefers-color-scheme`) on top
  of the existing no-hex-literals check. The trap is picking the surface token that *looks*
  right in whichever theme you have open: `--surface-raised` is a lift above `--surface-card`
  in the dark palette but is three hex points off it in the light one, so a "firms on hover"
  written as `raised` becomes "vanishes on hover" in daylight. Reach for the token that
  carries the intent — `inset` = recessed/further from the card (and the hover fill used
  throughout `PrimaryScreen.css`), `card` = lifted.
- **Cross-project rail status is a poll, and that is deliberate** (`stores/use-projects-status.ts`).
  There is no server-side cross-project status endpoint, and 13 §11 scopes desktop as
  frontend-only over existing contracts — so the hook reads each *non-selected* project's
  board on a slow interval (`fetchProjectBoard`, which names its project rather than reading
  `activeProjectId`) and derives state locally. The selected project is never polled (its
  board is live). Costs nothing for a single-project user, nothing while the tab is hidden.
  **If projects-per-user grows past a handful, add one server status endpoint — don't tighten
  the interval.**
- **`useProjectsStatus` keeps a module-level `lastKnownStates` cache, and it is load-bearing.**
  `CurrentProjectProvider` keys its subtree by the current project id so a switch tears the
  stores down and re-opens the stream (12 §4.1) — and the desktop shell rides *inside* that
  subtree, so the hook remounts on every switch. Seeding from `{}` would blank every other
  project's mark for one round-trip each time, at exactly the moment the user is looking at
  the rail.
- **The board and feed stores bridge the same remount through `stores/project-cache.ts`**, for
  the same reason and with the same justification (a derived cache of server-owned snapshots,
  module scope because the remount is the point, per-JS-context, memory-only, bounded to 8
  projects LRU). Each store captures `getActiveProjectId()` once at mount, seeds its state
  from the cache in a **lazy `useState` initializer** — not an effect, which fires after paint
  and would still flash one blank frame — and writes back through its single funnel
  (`applyBoard`; the feed's `remerge`). Three rules when you touch it: the cache **never
  replaces the fetch** (both stores still load on mount and refresh in place); the feed caches
  its **optimistic suppressions** too (swipe dismissals, accepted/deleted ticket hides) or a
  card the user just dealt with flashes back for a round-trip; and the feed **restores the
  frozen last-seen divider** rather than re-freezing it, since a switch is not a new session.
- **`loading` on both stores is what a shell renders the wait from**, and it stays true through
  the refresh that runs *behind* cache-seeded data — that is the whole point, not an edge case.
  `DesktopScreenView` renders it as one faint line above the feed and **withholds "All quiet."
  while it is up**: the resting line is a statement that we asked and there was nothing, and
  saying it mid-fetch teaches the user not to believe the line the screen most needs believed.
  The mobile shell keeps its own affordances (pull-to-refresh, the header's tickets spinner)
  and does not render this line, but it gets the cache for free.
- **The desktop resting state is the phone's, at desk size: the bell mark over centred
  text.** `desktop-rest` leads with `/kiln-mark.svg` at 72px (`desktop-rest-mark`; the phone's
  `feed-empty-mark` is the same asset at 64px) and is the ONE block on this screen that is
  centre-aligned — everything else is a ragged-left column of cards to be scanned, while this
  is a single composed statement with nothing to line up with. It is also the only accent on
  screen besides the rail's `needs-you` dot, and it gets there without touching the CSS accent
  budget (the red is baked into the asset, not a `var(--accent)` rule) — so
  `stylesheet-discipline.test.ts`'s "exactly one accent rule" case still holds. If a future
  ticket wants the resting view quieter, retint the mark, don't add a second accent rule.
- **Deliberately NOT ported to the desk** (all four are spec calls, not omissions): swipe /
  per-card dismiss and the bulk clear (13 §6 + open Q3 — the brain curates, 08 D1),
  pull-to-refresh (a touch gesture), and the header's ticket dropdown (board mechanism,
  13 D2). Don't "restore parity" without reopening those questions.
- **The desk shell is ALSO a touch shell, and a short feed has to bounce.** The breakpoint is
  width-only (13 §11), so a tablet in landscape is ≥1024px and gets *this* layout driven by a
  finger — which is where "the feed doesn't move when there's little in it" was reported. A
  scroller whose content fits has nothing to scroll *and no rubber-band to give*, so the desk
  now carries the phone's answer: one wrapper, `[data-role='desktop-feed-scroll']`, holding
  everything the region scrolls (the cards or the resting block, **and** the pinned "Show
  earlier"), held `min-height: calc(100% + 1px)` under `@media (hover: none) and (pointer:
  coarse)`. Four things to keep:
  - **The control must stay INSIDE the wrapper.** The wrapper is its containing block now, so
    the sticky half of its anchoring hangs off it; lifted out to sit beside the wrapper it
    would stick to a box it isn't in, and the wrapper's extra height would become scrollable
    slack the size of the control.
  - **The wrapper must stay a flex column with `flex: 1 0 auto`** — it takes the region's free
    height and hands it straight on to the resting block's own `flex: 1 0 auto` and the
    control's `margin-top: auto`, which only bites inside a flex column.
  - **Coarse pointer only.** With a mouse there is no bounce to earn back and the pixel buys a
    permanent scrollbar on every window.
  - **Never pair it with `overscroll-behavior` on the feed** — iOS WebKit reads `contain`/`none`
    as "no elastic bounce either" and would suppress exactly what the pixel unlocks. Chaining is
    dead-ended at the document instead (html/body locked + `overscroll-behavior: none` in
    tokens.css). Same reasoning, at length, in the phone's `[data-role='feed']` rule.
  This IS measured now, in `tests/layout/desktop-shell.spec.ts`. Touch is a *context* option
  in Playwright, not a viewport, so the coarse-pointer pass is its own `test.describe` with
  `test.use({ hasTouch: true, isMobile: true })` — a `setViewportSize` on the shared page
  cannot get there. It asserts the media query actually matched **first** (everything under
  it passes trivially against a plain desk window), then the region's overflow: >0 on a
  resting feed at 1194×834, exactly 0 on a 1280px mouse window, `overscroll-behavior-y`
  still `auto`, and "Show earlier" still inside the wrapper.
- **The desktop composer does not use the voice store's `keyboardMode`.** That toggle is
  modal (entering stops the mic) because a phone has room for one input at a time; a desk
  doesn't have that constraint, so the field and the mic coexist and the user picks
  per-utterance.
- **`DesktopComposer` holds NO text state — the field IS the store's transcript** (09 §4a).
  It renders `settledText` and writes every keystroke back via `editTranscript`; focus is
  `beginEdit`, blur is `endEdit`, and Enter/Send is `sendNow` for typed and spoken alike
  (`submitText` is now the phone's `keyboardMode` only). It used to keep a local draft and
  *cancel* the armed auto-send on focus to avoid firing stale words — which is exactly what
  made a correction cost the user their send. Don't reintroduce a draft copy: one buffer is
  what lets the countdown pause and resume instead. `data-hearing` (the two-tone heard block
  under a transparent textarea) is now gated on `!editing`, so mid-edit the same words are
  simply in the field — the heard block and the input are deliberately identical type/box so
  that swap is invisible.
- **Every view of the transcript is a view of the same flag.** `Dock`,
  `TicketDetailTranscript` and `DesktopComposer` all swap to a field when `editing` flips,
  and the two mobile ones are mounted at once while the sheet is open — so each keeps a
  `startedEditRef` and only the surface that was tapped takes the caret. The sheet's line
  also ends the edit on unmount (closing a sheet over a focused field fires no blur, and a
  stuck `editing` freezes the auto-send forever).
- **jsdom does no layout, so the desktop geometry is measured in a browser** — the
  three-column shell, the feed's reading measure at the
  narrowest desk window, the composer's mic-glow reserve and the toast's cap are all in
  `tests/layout/desktop-shell.spec.ts`; reduced-motion suppression and no hex/rgb literals
  (which would fork the palette instead of re-pointing tokens) stay in
  `styles/stylesheet-discipline.test.ts`, because they are claims about the source.
- **Ticket detail is a RIGHT-SIDE panel at a desk, a bottom sheet on a phone** (13 D7a
  amending D7), and the split is one prop plus one attribute:
  - **`placement` (`'bottom' | 'right'`, default `'bottom'`) is the JS half.** It is handed
    straight to vaul as its `direction`, which is what derives the entrance, the closed
    transform *and* the drag axis — so the edge is stated exactly once, in
    `DesktopScreenView`'s `<TicketDetail placement="right">`. Never re-state a slide in
    CSS to move the sheet: vaul writes those as *inline* transforms keyed to its own
    open/closed state, so a CSS `transform` is ignored (inline wins) and, forced with
    `!important`, strands the panel permanently open.
  - **`<body data-shell="desktop">` is the CSS half.** The shell's mount effect publishes
    the JS shell decision (it is now the *only* thing that effect writes, see D6a above),
    because `TicketDetail` portals out of the shell's subtree and no descendant selector
    can reach it. `body[data-shell='desktop']` rules in `DesktopScreen.css` stand the
    panel up against the right edge (`top: 0` + the base rule's `bottom: 0`, `left: auto`,
    `right: 0`, `width: min(460px, …)`, `max-height: none`) and drop the scrim to
    `transparent` — the panel exists so the feed and working strip stay *visible*, and
    dimming them takes back what moving it to the edge just bought. The rule must not
    declare `bottom` (that is the edge vaul translates away from) and must drop
    `border-top`/`border-right` rather than writing `border: none` + a new `border-left`,
    which would out-specify and erase a blocked ticket's tinted border. The resting
    geometry — right edge, full height, narrower than the window — is measured in
    `tests/layout/desktop-shell.spec.ts`; the mobile sheet is untouched because omitting
    the prop changes no DOM at all.
- **`FeedCardItem` takes a `moreLabel`**, defaulting to the mobile "tap to see more"; the
  desktop shell passes `"more"`. Only the text node changes, so every mobile DOM/image
  snapshot stays byte-identical — which is the pattern to follow for any other copy that is
  a phone word at a desk. Don't fork the component.
- **A shared popover inherits its ANCHOR too, and the anchor is a fact about the shell.**
  Inheriting the mobile sheet's unscoped rules is the point (above), but a dropdown's
  `top`/`right` encode where its trigger sits — and the two shells put the same triggers in
  opposite corners. The bell is a worked example: `NotificationSettingsMenu` is one
  component, mounted in the phone's top-right header cluster and in the desk's bottom-left
  rail foot, and the mobile "open down and to the left" anchoring aimed it off the bottom
  *and* off the left of a `100dvh` shell that cannot scroll it back — invisible, not merely
  misplaced. `DesktopScreen.css` re-anchors it to the bell's bottom-left corner (`top`/
  `right: auto`, `bottom: calc(100% + 8px)`, `left: 0`, `transform-origin: bottom left`).
  Two things to carry to the next one: release BOTH mobile anchors or the panel stretches
  between them instead of moving, and **re-state the open-state `transform` at higher
  specificity** — the desktop closed rule and the mobile `[data-open='true']` rule both weigh
  (0,2,0) and this file loads second, so without it the closed transform wins and the panel
  opens stuck out of place. Scope under `[data-role='desktop-screen']` rather than editing
  the mobile rule: a resize swaps shells with both stylesheets still loaded.
- **The working strip (`WorkingNow.tsx` + `working-now.ts`, 13 D9) reads the BOARD, not the
  feed.** It answers "which tickets are being worked right now" with one row per ticket in
  `board.working`, and it has to come off the board because the feed is the brain's curated
  narration (08 D1) — a ticket can be worked for an hour without earning a card. Four
  things about it are load-bearing:
  - **It renders above `[data-role='desktop-feed']`, in flow, not as the column's first
    row** — same call `SystemAlertBand` makes on mobile. Inside the scroller it would be
    the first thing lost when you scroll back through history, which is the whole problem
    it was added to fix. `DesktopScreenView.test.tsx` and the smoke script both pin it.
  - **Rows sort by `state_changed_at` ASCENDING**, so a ticket picked up now appends at the
    bottom and nothing already on screen moves. Newest-first — the feed's rule — would
    reflow the strip under the eye every time an agent starts something.
  - **A row's status comes from `board.agents`, not from the column.** A ticket parked in
    Working with a dead session behind it renders "failing"/"stopped"; `building` is the
    expected case and deliberately renders no word at all.
  - **`active` and the ticket list are separate props** because they disagree in both
    directions: the brain thinks (`thinking`) with nothing in Working, and a board snapshot
    can name a working ticket before the feed summary's `building` catches up. Either
    lights the strip; only the list names anything.
- **The rail carries a working COUNT, and it must never paint one** (13 D9). `RailProject`
  has a required `working: number` and `railHint` folds it into the row's `title` plus the
  mark's visually-hidden text — nothing else. Two reasons it exists at all: 13 §8 rules out
  badges and counts by name, *and* `deriveProjectState`'s precedence means a project that is
  blocked and building reads only as `needs-you`, so without the hint the running work
  vanishes. `useProjectsStatus` therefore returns `ProjectStatus` objects (`{state,
  working}`), not bare state strings — the module-level cross-remount cache holds those too.
- **`tests/layout/desktop-shell.spec.ts` is what can see the layout**, and it is in the
  gate: it serves the client from its own dev server, stubs every `/api` call, and measures
  the shell at a desk viewport and at the 1024px threshold. It catches the class of bug a
  CSS-string assertion cannot — regions that render but lay out wrong. It also
  covers the resting state (`cards: 0`) — "Show earlier" at the foot rather than under the
  resting text. Two things it does NOT cover, both of which the retired hand-run script did:
  the loading line's geometry mid project-switch (it needs the board/feed reads held open),
  and the read that the previous project's cards are still under it rather than the window
  having gone blank. Add those to the harness if you touch that path.
- **Sharing a rule is not sharing a clock — marks that must pulse together also need
  `--pulse-phase: shared`.** This column has now been fixed three times, each fix real and
  each one revealing the next layer: matching the *tempo* (`e078df2`), then giving the head
  the rows' actual `status-dot` so one rule paints both (`ddafd67`), and they *still* drifted.
  A CSS animation's timeline starts when **its own element** starts animating, so one shared
  declaration still runs from a different start per element — the head live from the panel's
  first pass, each row live from when that ticket was picked up. Identical marks peaking apart
  is the worst of the three readings: the eye tracks the shimmer between them instead of
  either mark. `src/pulse-phase.ts` (installed once in `main.tsx`) pins every opted-in
  animation's `startTime` to the document timeline, so phase becomes `timelineTime % duration`
  — a function of the clock, not of mount order. **The opt-in is a CSS custom property on the
  rule that declares the `animation`**, not a list of keyframe names in the module: the rail's
  project dot runs `kiln-breathe` at its own slower `--breathe-duration`, alone in a column
  with nothing to keep time against, and a name list could not tell it apart from a mark that
  wants the sync. Costs one frame — a new mark paints once at progress 0 before joining the
  clock, which the keyframes put at their quiet end. Because the opt-in sits on the shared
  `[data-status='building']` rule, the phone's list, header menu and kanban cards come along.
The opt-in lives on the one rule both surfaces render, so there is exactly one place to
  state it and none to forget; jsdom has neither `AnimationEvent` nor
  `getAnimations`, so the module is inert under the DOM tests and `pulse-phase.test.ts` fakes
  both seams. Verified in real Chromium: 0.38 of a cycle apart before, 0.000 after.
  **When you unify two animated marks, check the phase as well as the look** — a CSS-string
  test can prove they share a declaration and tell you nothing about whether they peak together.
  That check is in the gate now: `tests/layout/status-mark.spec.ts` reads every breathing
  mark's live animation and asserts one `startTime` (0, the timeline origin) and one
  `currentTime` across all of them, in a single `evaluate` so it is one instant rather than
  two. The same file holds the mark to the sheet it summarises — the desk head, its row, and
  the ticket's own detail badge compared as **resolved colours**, which is the claim the old
  `status-mark.test.ts` could only approximate by matching two token *names* across two
  stylesheets.
- **A glow is geometry too — an opaque band anchored to a region's edge will cut it.** The
  listening mic radiates a box-shadow ring ~20px past the button's edge (`kiln-mic-glow`),
  and the activity row is anchored to the composer region's *top* edge carrying an opaque
  `--surface-page` fill at z-index 6. The desktop composer sat flush against that edge, so
  every toast sliced the ring off along a hard horizontal line — invisible to the CSS-string
  assertions and to every DOM test, because a box-shadow occupies no layout box. The phone
  was fine only by accident: the dock's own padding already stands the mic off its top edge.
  Fix is clearance on the containing block (`[data-role='desktop-composer-region']`'s top
  padding), never a z-index that lifts the mic *over* the band — that just moves the collision
  and paints the halo across the pills. **When you anchor anything to the edge of a region
  holding the mic, budget the glow's reach, not the button's box.**
  `tests/layout/desktop-shell.spec.ts` measures the reserve directly: the composer's top
  edge stands at least the glow's reach below the region's top, which is the edge the band
  anchors to.

## `/kanban` — the second desktop shell (board view)

`/app` and `/kanban` are two views of one board store, behind the same `SessionProvider` +
`SessionGate` + `CurrentProjectProvider`. `/app` answers "what should I look at now" (the
brain's curated feed, 08 D1); `/kanban` answers "where does everything stand", which the feed
structurally cannot — a ticket can sit in Ready for a day without earning a card. Anchor:
`components/KanbanScreen.tsx` (seam) → `desktop/KanbanScreenView.tsx` + `desktop/Kanban.css` +
the pure `desktop/kanban-board.ts`.

- **The rail is shared as a COMPONENT, not copied.** `desktop/DesktopRail.tsx` was extracted
  from `DesktopScreenView` for this and is mounted by both. The brief was "identical to the
  desktop app's sidebar", and a second `<aside>` agrees on the day it is written and drifts on
  the first change to either screen. Its markup is unchanged from when it was inlined, so the
  existing DOM tests, the CSS, and `tests/layout/other-shells.spec.ts` all still find it.
- **The kanban root wears `data-role='desktop-screen'` with `data-view='kanban'`**, rather than
  a role of its own. That is what makes it the same shell: the `html:has(...)` viewport lock,
  the `*` box model, the visually-hidden helper and — easiest to miss — the notification bell's
  re-anchoring (it opens up-and-right from ANY desktop shell's rail foot) all apply for free.
  `Kanban.css` then overrides exactly one shell declaration, `grid-template-columns`, to two
  columns. A second root role would have meant restating four rules that must never disagree.
- **`useDesktopShellFlag()` (in `use-desktop-layout.ts`) publishes `<body data-shell="desktop">`**
  and is called by BOTH desktop shells. It used to be an effect inlined in `DesktopScreenView`.
  It is nothing to do with theming — it is how the ticket sheet, which portals to
  `document.body`, learns it is a right-edge panel and not a full-bleed phone sheet. A second
  shell that forgot it would open its panel as a bottom sheet across a 1440px window, and
  nothing in the DOM gate could see that.
- **Columns are the five states, in pipeline order** (Shaping, Ready, Working, Blocked, Done) —
  deliberately NOT the mobile board's three zones (backlog / developing / done), which
  compress five states for a phone's width. **Ticket order inside a column is the server's,
  untouched**: `ready` arrives in exact pull order (03 §5), so re-sorting it locally would
  destroy the one column whose order carries information.
- **No drag-and-drop, and that is 07 D5 rather than an unfinished feature.** Every transition
  belongs to the brain; a draggable column would be a second, contradicting source of truth
  about the same states. Cards open the SAME `TicketDetail` the feed opens, `placement="right"`,
  with the same actions wired the same way. Don't grow a detail pane of its own.
- **A card's status mark comes from `board.agents`, not from its column** (same rule as the
  working strip), and is `null` — no mark at all — for a ticket with no worker bound. A Ready
  ticket painting a session dot would be inventing a session.
- **The accent budget is spent once here too, on BLOCKED** (a 2px left edge). That is not a
  second budget beside `DesktopScreen.css`'s `needs-you`; it is the same rule — "a person is
  needed for a decision" — applied to the one thing on this board that qualifies. Asserted in
  `styles/stylesheet-discipline.test.ts`, along with no-hex/no-rgb, no theme selector, and
  no animation.
- **The count in a column head is the one number this shell allows itself.** 13 §8 rules out
  badges in the *resting* screen, whose premise is that there is nothing to manage; a board's
  premise is the opposite, and a column's depth is the fact it exists to report. Faintest ink,
  never tinted, tabular figures.
- **`tests/layout/other-shells.spec.ts` is what can see this layout**, and it is in the gate:
  the columns on one row, each heading held at its column's top, and the overflow belonging to
  the LIST rather than the column — none of which jsdom or a CSS-string assertion can see. The
  clamps and the accent's single edge are not measured; if you touch either, that spec is where
  the assertion goes.
