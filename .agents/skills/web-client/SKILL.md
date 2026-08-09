---
name: web-client
description: Use when working in the frontend — the thin, disposable, mobile-first client. The primary screen (08) is a feed + dock over a live board, with a desktop shell and a /kanban board view beside it; voice (STT) and Web Push have shipped, and Kiln does not speak. A landing page, sign-in, and per-user projects sit in front of it. Holds no authoritative state. Anchor /frontend. Specs 07, 08, 13.
---

# Web client (v1 shape decided by spec 07, primary screen by 08, desktop by 13)

## What it is

A deliberately thin, disposable, **mobile-first** surface that renders the board live and
carries the conversation with the brain. **It holds no authoritative state** — the board and
feed stores are caches of server-owned snapshots, and the transcript is server-owned.

Voice (STT in front of `POST /api/message`) and Web Push have both **shipped**. **There is no
TTS: Kiln does not speak** — `say` is on-screen text only.

**This skill carries the cross-file rules and the traps.** Single-file mechanics — why a
particular `bottom` is negative, why one taxonomy has two names — are documented *at the code*,
in long headers on `feed-kinds.ts`, `feed-model.ts`, `ticket-intents.ts`, `project-cache.ts`,
`use-keyboard-viewport.ts` and beside the rules in `PrimaryScreen.css`. Read those; don't
re-narrate them here.

**Stack.** Vite + React + TS strict. **TS escape hatches are banned** (`any`, `as`,
`@ts-ignore`, non-null `!`, unused symbols) and the gate enforces it — narrow `unknown` with
guards instead. Types come from the wire schema; never hand-write client↔server types.
**Dependencies are gated on explicit user approval, default zero** (07 D4 — not a flat ban:
block on the user before adding any library). Approved so far: `vaul`, `react-markdown` +
`remark-gfm`, `@sentry/react`, `react-router-dom`, the `@fontsource-variable/*` fonts.

**Transport.** SSE + POST (04 D6). `transport.ts` is the only code that knows URLs.
`GET /api/stream` carries four events — `board`, `say`, `feed`, `activity`. Reconnection is
EventSource's native retry; the first board event is the resync, each stream reopen costs one
feed refetch, and a stale board is **dimmed but visible, never blanked**.

**The board is read-only — no drag-and-drop** (07 D5). Every transition belongs to the brain; a
draggable column would be a second, contradicting source of truth about the same states.

### Routes (`main.tsx`)

| Route | What |
| --- | --- |
| `/` | `DefaultRoute` — the marketing landing for a browser tab; an installed web app (whose `start_url` is pinned here) is redirected to `/app` |
| `/landing`, `/landing-2` | aliases pinned to the landing page for everyone |
| `/app` | the **primary screen (08)** — feed + dock over the board, behind `SessionGate` |
| `/kanban` | the board view (13) — same stores, second desktop shell |
| `/dashboard/*` | the signed-in account view (11) |
| `/signup` | the sign-up rehearsal (see below) |
| `/projects` | `ProjectsManager` |
| `/onboarding` | the onboarding guide — a stateless styled page, `docs/onboarding.md` on the web |
| `/beta/pending` | the private-beta screen, **public** (outside the session gate) |
| `*` | 404 |

**`/debug` and `App.tsx` are gone** (removed in `07638df`). The 07 "board on top, chat below"
developer view no longer exists — if you meet a reference to it, it is stale. Every route
except `/` is code-split.

## Standing principle: assert layout as geometry, in a browser

`tests/layout/` (in `make check`) renders the real client with every `/api` call stubbed and
measures boxes and paint order. **jsdom performs no layout**, so the unit gate can see which
elements render and never where they end up.

The repo used to slice rule bodies out of stylesheets with `?raw` and match them as strings. A
string test passes when the rule is present and the layout is still broken — which is exactly
how the same "Show earlier"/toast overlap shipped **five times** with everything green. The one
thing still asserted about CSS *text* is what a sheet may not **contain** — no colour literals,
no second theme switch, no literal z-index where the layer scale has a rung
(`styles/stylesheet-discipline.test.ts`) — because you cannot observe the absence of a colour
in a rendering. **Anything positional goes in `tests/layout/`.**

## Standing principle: text earns its place

*Applies to every surface in `/frontend`. A default to design against, not a lint rule; the
reviewer is you.*

**A glyph whose meaning is unambiguous ships without a label.** A × beside the word "Close", a
trash can beside "Delete", a gear beside "Settings" — the word tells a sighted reader nothing
the glyph didn't, and it costs a control's worth of width on a 390px screen.

The accessibility of that is not optional and not hard: an icon-only control takes its
accessible name from `aria-label`, and `title` gives a pointer user the same word on hover. The
icons in `dashboard/icons.tsx` are all `aria-hidden`, so **without an `aria-label` an
unlabelled icon button is nameless to a screen reader** — a real bug, and why the two rules
travel together. Test queries stay `getByRole('button', { name: 'Close' })`: **the name is the
contract, the visible text is not.**

The exceptions are genuinely ambiguous glyphs — one that could plausibly mean two things, or
one carrying a *value* rather than an action (a model name, a branch, a count). "Would a
first-time user hesitate?" is the test. Styling follows: an icon-only button is square and a
ghost at rest, not a padded pill with its label removed.

**The same economy applies to prose.** Don't write help copy for something the context already
says. A danger-zone paragraph under a delete button says nothing the confirm dialog doesn't say
at the moment it matters; a hint under a field whose label already names it is noise the user
learns to skip — worse than absent, because it teaches them to skip the hints that *do* matter.
The bar for any string on screen: **it changes what someone does.** If it doesn't, it is weight
— on the screen, in the DOM, and in every future diff that has to keep it true.

## Standing principle: bottom-anchored UI grows upward and overlays; it never pushes

The bottom of the primary screen is a stack of layers that all grow **upward** over the feed:
the dock (mic controls, in flow) is the base, the live transcript overlays just above it, the
notification hub (toasts / "Kiln is thinking") sits on top.

- **The dock is not a fixed height.** It expands as the transcript grows, so anything anchored
  above it must anchor to its *current* top — the dock publishes `--dock-overlay-height` and
  the hub offsets by it. Geometry keeps the layers apart; z-order is only a backstop for
  mid-resize frames.
- **A transient notice must move nothing.** A toast arriving is not a layout event, and
  "nothing" means the board — not merely the one control pinned to its foot. Padding on a
  scroll container does two things and only one is wanted: it extends the scroll AND shrinks
  the content box. So each shell's scroll wrapper hands the growth straight back
  (`--feed-overlay-slack`) and the sticky control subtracts the same slack from its own
  `bottom`. **Adding an overlay var *positively* is the trap** — it applies the clearance
  twice. The arithmetic and its history are commented at the rules in `PrimaryScreen.css`.
- **The layer order is a lookup, not a paragraph.** `--layer-*` in `styles/tokens.css` states
  it once. **Read a rung; never write a number.** `--layer-feed` exists to be named and never
  applied: anything the feed pins into the bottom reserve must carry **no `z-index` at all**,
  or it lifts itself over the dock's overlays. That single mistake is the overlap fixed five
  times.
- **A permanent problem reserves its height; a transient one floats.** `SystemAlertBand` is
  the deliberate opposite of a toast: rendered **in flow** at the top of the dock region,
  driven by the board snapshot's `alerts`, persisting until the condition clears. It returns
  `null` for an empty array, so the healthy layout is byte-for-byte unchanged. Keep it
  **error-agnostic** — it renders each alert's `detail` verbatim and never switches on `kind`,
  so the same band serves any future persistent failure.
- **Activity pills are one tap target, never two.** No pill carries a close control in any
  state. One button fills the pill and the tap means whatever that pill has left to do: route
  to a ticket and dismiss; expand in place *if and only if* its clamp is actually hiding
  something; else dismiss on the first tap rather than opening onto an identical copy of
  itself. **"Can it expand?" is measured, not assumed** (`useClampOverflow`) — the same
  utterance overflows on a phone and fits on a desk, and both render the same component.

**When you add any new bottom-anchored surface**, decide its place in this upward stack, anchor
it to the *dynamic* height of the layers below it, take its rung from the scale, and add the
assertion to `tests/layout/bottom-stack.spec.ts`. **A rule written down here and nowhere else
is not a rule.**

## Standing principle: the soft keyboard OVERLAYS the app

`index.html` asks for `interactive-widget=overlays-content`, and that is where the model
starts. Chrome Android's default (`resizes-content`) shrinks the *layout* viewport — and with
it `dvh` — as the keyboard animates, squeezing the whole `100dvh` column upward and reflowing
the feed under the user's eye. iOS Safari has no such mode; it shrinks only the *visual*
viewport. The key lines the two engines up, so **one JS path serves both**.
**Don't reintroduce `resizes-content`**: it looks like a smooth native follow on a screen with
nothing but a dock on it, and it is the reported bug on a screen with a feed in it.

`use-keyboard-viewport` publishes the keyboard's overlap as `--keyboard-inset` on the document
root, frame-synced to the visual viewport and tracked continuously so the OS animation is
ridden rather than snapped at. Everything else reads the var. **Four consumers, each a
different answer to the same question — add the fifth by deciding which of these it is, not by
inventing a sixth mechanism:**

| Surface | How it clears the keyboard |
|---|---|
| dock region (phone) | `translateY(-inset)` — a compositor transform, no reflow |
| desktop composer region | the same transform |
| feed / desktop feed | `+ var(--keyboard-inset)` in `padding-bottom` — the covered strip becomes scroll room |
| ticket detail sheet | `bottom: var(--keyboard-inset)` + a `max-height` reduced by it — the panel stands ON the keyboard |

Two asymmetries in that table are the whole design:

- **Padding below the content, never a height change — for a surface anchored at the TOP.**
  Growing the reserve *under* what is already on screen moves nothing, so opening the keyboard
  costs no scroll position. A height/`dvh` rule keyed on the inset is the regression.
- **…and the sheet is the exception, because it is anchored at the BOTTOM.** Padding under a
  `bottom: 0` box doesn't sit under the content, it *inflates the box upward* — the sheet grew
  300px, threw a short ticket to the top edge and left a keyboard's worth of blank paper below
  the controls. A bottom-anchored panel **moves** instead. It cannot use the dock's
  `translateY`: vaul owns this panel's transform as an inline style.
- **The keyboard's strip is the ONE reserve not handed back through `--feed-overlay-slack`**,
  and the contrast with the section above is the point. A band is *transient*, so the board
  holds still under it. The keyboard's strip is *gone* for as long as it is up, so the feed
  SHOULD shorten to what is left. Adding the inset to the slack is the mirror image of the bug
  the slack exists to fix.

Measured in `tests/layout/soft-keyboard.spec.ts`, which drives `--keyboard-inset` directly
(`visualViewport` is read-only). **It also asserts the viewport meta itself**, because that is
the one thing in this model nothing else in the gate can see: with `resizes-content` back,
every other assertion still passes and the real phone still reflows.

## The shell architecture — four shared layers under three shells

`/app` renders **one of two presentational shells** by viewport width (`PrimaryScreen.tsx` is
the switch); `/kanban` is a third over the same machinery. That is 13 D10: one responsive tree,
with the shells sharing **stores, intents, the feed reading model and overlay behaviour, and
differing only in DOM shape and CSS.** The rejected alternative was one tree restyled by media
queries — the two layouts don't share a DOM, and forcing one to be both is how a desk rule
breaks the phone's layering.

**Sharing only the stores was tried and it drifted**: 187 hand-copied lines between the two
feed shells, a third partial copy in `/kanban`, and the same conceptual layout bug fixed five
times in alternating shells. Work belongs in the **lowest layer that fits**:

| | Module | What it owns |
|---|---|---|
| **L0** taxonomy | `components/feed-kinds.ts` | What a card KIND is and means — the one place the six kinds are enumerated |
| **L1** intents | `components/ticket-intents.ts` | What the client may DO to a ticket, and how a failed write recovers |
| **L2** reading model | `components/feed-model.ts` | What there IS to show — membership, order, divider position, seen-ness, dismissability |
| **L3** behaviour | `components/use-ticket-overlay.ts` + `TicketDetailHost.tsx` | What a shell REMEMBERS — the open ticket, the deep link, the voice-active flag |
| **L4** shells | `PrimaryScreenView` / `DesktopScreenView` / `KanbanScreenView` | What it LOOKS like. Markup, CSS, platform-only affordances |

**The rule: a shell file contains no `function` declaration and no `useState` above its
component body.** Computes what to show → L2. Remembers something → L3. Writes to the server →
L1. Two things enforce it, because a documented convention is exactly what these files already
had: an **eslint rule** scoped to the three shell files (it fires on the *fourth* shell before
anyone reviews it), and **`feed-shell-conformance.test.tsx`**, a `describe.each` over shell
adapters asserting each *shared* rule once against both DOMs. A new shell joins by adding a
row. When purity pushes back, give that view its **own** model module — never push presentation
copy into a module the other shells share.

**Never write `card.kind === '…'`. Ask `feed-kinds.ts`.** The taxonomy is a matrix with a row
per kind and a column per decision. **The compile error is the feature**: a
`Record<FeedCardKind, …>` is missing a property the moment the wire union grows, so a new kind
breaks that one file with every unanswered decision listed. `matchKind(kind, arms)` is the
exhaustive switch for a decision that is one view's own — **never a `switch` with a
`default`**, which answers a plausible wrong thing instead of failing the build.

**Two card taxonomies, and they must never be merged.** `notificationId()` covers all four
notification-backed kinds (the store's accumulation, the history cursor, the swipe's retract
id); `authoredUpdateId()` covers only update/preview — claims about what Kiln *said*, driving
the last-seen divider and the seen de-emphasis. Both were once called `updateId`. Merging them
by name type-checks and silently slides the "Earlier" divider onto the mechanical poke and done
cards. `feed-model.ts`'s header explains it at length.

**What shared code must NOT decide.** `loading` is desktop-only — not in the shared reading
model at all, so no shared code can start rendering a line on the phone. `isEmpty` is a *fact*;
whether to withhold "All quiet." while loading is each shell's call. The divider's *index* is
shared, its wrapper element is not. And "Show earlier" must stay last **inside each shell's own
scroll wrapper** — its `position: sticky` hangs off that containing block, which is the bug
fixed five times.

## The feed

- **Seen cards collapse away; one "Show earlier" brings them back** (08 D2‴). The default feed
  shows only what the user hasn't caught up on. There is exactly one foot-of-feed control, and
  it reveals the collapsed cards then keeps paging older history under the same label.
  - **The boundary is the seen FLOOR, an id, not a per-card clock.** Unseen ⇒ above the floor
    ⇒ never collapsed, at any age. **Never key a filter here on `created_at`.**
  - **The floor is per VISIT, and that is the load-bearing part.** Rendering a card acks it
    seen within a round-trip, so collapsing on the raw ack would erase a notification a second
    after the user opened the app to read it. It is read at the first snapshot of a visit and
    again on return-to-visible. Nothing moves while the user is looking; what they read is gone
    when they come back. That return-to-visible advance is also the only thing that declutters
    a surface which is never reloaded — the desktop window left open all day.
  - **A project switch is NOT a new visit** — the floor rides in the cached feed and is
    restored verbatim, or a switch back would blank a feed the user was reading a moment ago.
  - **Everything merges through `remerge()`.** The store has three stacked suppressions
    (optimistic ticket hides, swipe dismissals, collapsed seen cards). Add a new one inside
    `mergeFeed` + `remerge`, **not at a call site**, or they drift apart depending on which
    mutation path fired last.
- **Swipe-to-dismiss** wraps a row **only** when the dismiss callback is wired AND the card is
  notification-backed — blockers and proposals are board state the brain owns and stay static.
  The clear is full-stack: optimistic hide, POST, spring back on failure, suppression pruned
  once the server-confirmed snapshot drops the id. A purely client-side hide would resurrect on
  the next snapshot. The fling-off completion is driven by a **timer, not `transitionend`** —
  under `prefers-reduced-motion` the transition is suppressed and `transitionend` never fires.
- **A card's body is Markdown — except the two that aren't.** The brain-authored body renders
  as Markdown (the 06 prompt tells it to write that way). The **done card's work summary stays
  verbatim text** — it is a commit message, and a renderer would fold its hard-wrapped lines
  into one run and eat a leading `#`. The **proposal digest stays text** because it lives
  inside the button that opens the ticket, and block elements and nested links cannot go there.
  If a digest ever wants formatting, that means restructuring the click-through, not flipping
  the flag.
- The body element is a **`div`, not a `p`** (block children can't live in a paragraph), and
  every rule that dresses it keys off `data-role`, never the tag. **A link in the body is still
  a link** — the clamped body is the expand toggle, so its handler ignores presses landing
  inside an `<a>`.
- **The phone's tickets dropdown has NO head — the list is the panel's first content.** The
  heading is gone, mark included: the panel opens from a button that already reports what it
  holds and every row carries its own status mark, so the line above them named the list a
  second time and spent the panel's first 42px doing it. It had just been given the shared
  status mark keyed on the top ticket when it went; **don't reintroduce it as a lone mark with
  no word** — that history is in `HeaderStatusMenu.tsx`'s log. **Mobile only: the desk's
  in-progress panel keeps its head**, which is where `tests/layout/status-mark.spec.ts`
  measures the head-vs-row match; the phone's case there now holds the list's own marks.

## The ticket detail sheet

Read-only inspection over a read-only board as far as the ticket's *state* goes — Accept,
Delete and Poke express intent and route **through the brain**. Three things bypass it, each
for its own reason (the API-side table is in `runtime-and-api`):

- **The sandbox toggle** — a *setting on the ticket*, so round-tripping it through an LLM pass
  would be slow and non-deterministic for no gain.
- **The text edit** — skips the brain for the opposite reason: **an LLM pass is the thing being
  avoided.** Describing a wording change and letting the brain rewrite the ticket is the drift
  the affordance exists to prevent, so the typed text has to land verbatim. **Never "improve"
  this by routing it back through the brain.**
- **The sandbox overrides** (re-create / move to a free sandbox) — they exist so the user can
  reach *past* the orchestrator when a sandbox is wedged, and an override that waits on an LLM
  turn is not an override. Each is gated behind a `window.confirm` naming what is lost.

Consequences shared by all three:

- **Gates mirror the server's preconditions, they are not client opinions** —
  `EDITABLE_STATES` (shaping/ready) mirrors `ShapeTicket`; `SANDBOX_CONTROL_STATES`
  (working/blocked) mirrors "a worker is bound". **Widen the two together** or the sheet invites
  an edit the server answers with 409. Move's *presence* comes from the board snapshot the
  sheet already has, so the user is never walked into that 409.
- **None of them closes the sheet.** Every other action closes on tap; these are things done
  *while reading*, so they pass straight through without closing.
- **All are optimistically shown, time-boxed.** The value lives on the board snapshot, which
  only comes back over the stream, so the sheet renders a pending value over the ticket's and
  drops the overlay as soon as the snapshot agrees — or after a timeout if the write never
  lands. A failed write also refreshes the board so it snaps back at once.
- **Save sends only the fields that changed**, so editing the title can't clobber a body the
  brain rewrote while the sheet was open.

**The body IS the edit affordance — there is no pencil** (removed; don't reintroduce one).
Pressing the rendered Markdown swaps that region for the editor, so the words never move to be
changed. Four things make that safe:

- **A plain `div`, never a `<button>`/`role="button"`.** A button announces its contents as its
  own label instead of as the document they are — and the body is what the sheet exists to show
  — plus Markdown can contain links, which cannot live inside a button.
- **The keyboard/AT route is a separate control** ("Edit description"), clipped off-screen
  until focused. It is the *only* way in without a pointer, so it must stay tabbable —
  **clipped, not `display: none`**.
- **Three presses inside the body must NOT open the editor**: a click ending a *drag* (the body
  is inside the scroll region, so most fingers on it are scrolling), a click on an `<a>`, and a
  click ending a text selection (otherwise copying a ticket is impossible).
- **An empty body renders a placeholder**, because an editable ticket with nothing written
  would otherwise have nothing to press.

**The `<Drawer.Title>` stays mounted while editing**, visually hidden by a *clip* rule, because
Radix names the dialog by it. `display: none` is the one hiding style an accessible-name
computation may skip — don't "simplify" it to that.

### The sandbox affordances live behind one gear

They were a checkbox, two buttons and three paragraphs at the foot of the scrolling body; they
are now one gear at the end of the status row opening a dropdown.

- **Each item self-gates on its callback arriving**, so a read-only sheet renders no gear at
  all and is byte-identical.
- **An unavailable Move is HIDDEN, not disabled.** Inside a menu a dead line is pure noise, and
  there is no room for the "why it's greyed out" hint that justified a disabled button.
- **Escape must reach the menu before the sheet, and that takes `window` capture.** Radix
  (under vaul) listens for Escape in the capture phase **on `document`** and mounts first, so a
  `document` listener in *either* phase loses the race and the whole sheet dismisses. The menu
  listens on **`window`** with `capture: true` — the first node in the propagation path — and
  stops propagation, only while open. **Any future popover inside this sheet needs the same
  trick.**
- **A closed panel stays mounted** (so it animates both ways) and is taken out of the page by
  `aria-hidden`. Consequence for tests: a closed menu's items are *absent* from role queries,
  so every test opens the gear first.

### The footer swaps its right-hand controls; nothing on the row ever moves

The footer is left-group / right-group in both readings: the voice cluster at the bottom-left,
the trailing slot at the right. When a voice session goes live only **who holds the trailing
slot** changes — the state actions withdraw *as a group* and the cluster's own Send and ×
take their place. **The mic does not move, at all, ever.**

- **This is the fix for the original arrangement, so don't reintroduce it.** The cluster used
  to *travel* to the trailing end, which slid the mic out from under the finger that had just
  tapped it. A control that jumps when you activate it is the whole bug; "elements stay put" is
  the rule that replaced it.
- **The cluster spans the row and distributes its own children** (`flex: 1` +
  `space-between`). That one rule does all of it — no `order`, no margin flip, no position
  attribute. **If any of those come back, the mic has started travelling again.**
- **Send/× are one box and mic/keyboard-toggle are another**, because `space-between` over
  loose children would strand one in the middle of the row or fling it to the far end.
- **All three state actions share one gate, deliberately.** The trailing slot is one slot, so
  leaving any of them up just means Send arriving *beside* them and shoving them along. Each
  also earns it alone: Accept's slot is Send's, Delete is a destructive control appearing the
  instant someone opens their mouth, and Poke offers to nudge an agent while the user composes
  the message that would say it better.
- **`MicButton` is never re-parented and never re-ordered**, which keeps it from being
  unmounted mid-utterance — that would take its `setTicketContext` registration and its
  volume-glow rAF loop with it.
- **`TicketDetail` knows nothing about the voice store**, and that shapes the seam: a
  `useVoice()` consumer reports `onActiveChange(boolean)` up to the shell, which hands it back
  as a prop. It is a **boolean on purpose** — it fires twice an utterance rather than once a
  word, so neither shell re-renders on transcript churn. **Wire it in both shells**; the state
  is per-shell and forgetting one is the obvious way to ship this half-done.
- **A session is live when the mic is listening OR there are words on screen OR the user is
  typing** — not just the middle one. An end-of-turn final pauses the mic while the utterance
  sits armed in the grace window, and the footer must not snap back to Accept underneath a
  transcript the user is still deciding about.
- **The sheet's × is not the dock's.** The dock's discards the transcript and leaves the mic
  listening; the sheet's is the way *out* (cancel **and** pause), because that is what returns
  the footer to Accept. Don't "unify" them.
- **A view test mocking `useVoice` must rest at `micState: 'paused'`** — the app never opens
  listening, and a `'listening'` mock puts *every* sheet in the file into the speaking
  arrangement, so Accept vanishes from tests that never mentioned voice.

**Typed input rides the same arrangement**: the same Send and × in the same slot, pointed at a
draft instead of the transcript, with the panel above becoming the field. Voice stays primary —
the toggle is offered at rest and only at rest.

- **The draft lives in `TicketDetailHost`, because the sheet's dock renders it in TWO slots**
  (the field in the transcript node, its controls in the voice-actions node); the host is their
  only common parent. Both take it as a **required** prop, so a shell cannot mount half of it.
- **It is deliberately NOT the voice store's `keyboardMode`.** That flag is the DOCK's typing
  mode; flipping it from the sheet reaches through the scrim — the dock enters the mode with a
  second empty draft, its focus effect races the sheet's for the caret, and the mode outlives
  the sheet that opened it. Two surfaces, two drafts, two flags; what they share is the seam
  underneath (`submitText`).
- **The mic orb IS the way back to voice**, a rule in the hook rather than a button: tapping it
  resumes the mic and typed input stands down. The draft is scoped to the ticket, because the
  ticket's title is what the message is prefixed with.

**Poke is offered on every working ticket, idle session or not.** It used to be gated on the
agent session reading `idle`, which hid it for most of an in-progress ticket's life —
`building` is the normal reading mid-turn, and that is precisely when a user watching it go the
wrong way reaches for the nudge. The session status is too coarse to mean "needs nothing", and
poking costs nothing either way since the client only posts an intent and the brain decides.
**Don't reintroduce a liveness gate here.**

**Every footer glyph is the mic's disc**, off one rule whose numbers are copied from the dock's
mic; what distinguishes them is the glyph alone. None carries a `:hover` or a primary-surface
skin, and **both absences are asserted** — a touch device emulates hover, and a
primary-surface override at higher specificity is exactly how the row drifts apart on the one
surface the app renders. The mic itself is dressed in the *other* stylesheet (unscoped, so the
sheet picks it up wherever it is placed), which is the kind of seam an edit to one side walks
past: **if you restyle the mic, restyle these with it.** That now includes `dock-send` and
`dock-cancel` — the pair the state actions hand the trailing slot to mid-utterance — which came
over from the dock at 40px and are the mic's 54px disc on both surfaces, with two scoped
exceptions on purpose: the dock's keyboard MODE (`[data-mode='keyboard']`, the typing row — not
the toggle) holds send at 40px since there is no orb there to match, and the desktop composer
keeps its borrowed × at 30px.

## Dashboard, sign-in and onboarding (spec 11)

`/dashboard` is a separate surface — GitHub sign-in → first-run onboarding → settings with
credentials and live verify. It owns its own provider; the primary screen never mounts it.
**Phase 2 put the whole app behind a session**: `SessionProvider` + `SessionGate` resolve
`GET /api/me` before `/app` or `/kanban` mount, because every `/api/*` call is project-scoped.
Only the public routes stay session-free.

### One GitHub flow — sign-in IS the GitHub connection

There is **one** OAuth flow, **one** registered callback, and **one** path constant
(`GITHUB_CONNECT_PATH` in `src/auth/github-connect.ts`). **Import it — never restate the
literal.** It is deliberately *not* in `integrations-config.ts` with the other shared
credential facts: the landing page and the session gate link to it too, and must not pull the
dashboard's provider tables into their bundle to do it.

**Where the flow ends is chosen by which form of the constant you link.** Plain
`GITHUB_CONNECT_PATH` ends **in the app** — that is what "sign in" means, and the backend only
diverts to `/dashboard` when the user has no project and onboarding is genuinely next. The
`?next=dashboard` form is for the affordances that LIVE there, where the grant is a step in
something the user is already doing on that screen. Getting this wrong is invisible on a phone
(an installed web app relaunches at `start_url` and walks itself to `/app`) and stops a laptop
dead on the wrong screen — which is how the callback's old unconditional `/dashboard` survived
as long as it did.

The route redirects to the **GitHub App's authorize screen**, and the backend resolves the
installation behind it, so signing in already authorizes repo access to exactly the repos the
user picked. This replaced a split — a scopeless login route beside a repo-scoped connect —
that shipped a settings card pointed at the wrong one. **Never add a second GitHub app, flow,
callback, or path constant for repo access.** The `?setup=1` form is the **only** sanctioned
second link, and it is a *screen* request rather than a second grant: it goes straight to
GitHub's chooser, for the **already-connected** user changing accounts or repos. The plain
route cannot serve them — they have authorized already, so it completes silently and shows
them nothing.

**Two buttons, one flow.** The landing nav offers "Sign up" and "Log in" pointing at the
**same** path. The wording is the only difference and that is the design: a visitor knows which
of the two they are, and a bar offering only "Sign in" reads as closed to newcomers. **Do not
give them separate routes or query markers** — 11 D2a settled that two GitHub routes differing
only invisibly is how a call site ends up pointed at the wrong one. Both are plain `<a href>`,
never router `Link`s. **Both survive on mobile**: the bar used to hide the sign-in link under
720px, leaving a returning user on a phone with no way in from that page at all. Width comes
out of the buttons' padding instead — **and use `padding-inline`**, since `.kiln-btn` has no
`height` and `line-height: 1`, so writing the override as the `padding` shorthand zeroes the
vertical half and collapses both buttons to a 16px sliver. Every DOM test still passed; only
the browser caught it.

**There is no email capture anywhere.** The beta signup form, modal, endpoint and thanks page
are all gone. Signing up IS the GitHub grant and the beta list is written server-side from the
login, so nobody is asked for an address they have already proved they own. **Don't add a form
back.** A login not on the allowlist is recorded and redirected to `/beta/pending` — a
**public** route outside the session provider, for the obvious reason that everyone reaching it
was just refused a session. It is **not an error page**: the person did everything right and is
early, so it asks for nothing and offers no next step to chase.

**There is no free-text repo URL field, and no free-text token input.** Credentials are connect
cards (`Integrations.tsx`); the repo is picked from the connected account (`RepoField`). Secrets
stay write-only — an input never seeds from the stored value, only a `configured · …tail`
placeholder.

- `useGitHubRepos` is **user-scoped** (unlike the per-project sandbox catalog): mount it once
  per view and pass it down — one credential serves every project card.
- **Three render states, and the ordering matters:** `loading` shows a placeholder so the
  connect prompt never flashes before we know; `!connected` shows the connect link; connected
  shows the dropdown. **`connected: false` is a normal 200, not an error** — a failed *request*
  sets `error` and deliberately leaves `connected` alone, so a transient blip can't demote a
  working picker mid-edit.
- **An unmatched existing value stays selectable as `(current)` and is still submitted**, so
  editing an unrelated field can never silently unlink a project's repo. The snapshot picker
  makes the same guarantee.
- **Every render site and test must pass `github`** — a required prop on purpose, so no
  free-text fallback can drift back in.

### First-run setup is a guided flow, not a form

A signed-in user with no projects gets **three steps**, one per decision: connect GitHub →
choose the project → choose the provider (+ that provider's key), then Finish.

- **The ordering is load-bearing.** Step 2 can only list repos once step 1's credential exists;
  step 3 can only know *which* key to ask for once a provider is chosen. **Don't reorder or
  merge the steps** — that is the whole feature.
- **Step 1 usually arrives already satisfied**, since signing in grants repo access. Don't
  delete it as redundant: the disconnected branch still catches accounts that predate the merge
  or revoked the grant.
- **It reuses settings' controls, it does not clone them.** A second repo-picking or
  secret-entering implementation is how a free-text URL or a non-write-only secret drifts back
  in.
- **Finish writes the key BEFORE the project**, so a project never comes alive pointing at a
  provider whose credential hasn't landed. The key field auto-saves on blur *and* Finish commits
  it, so a guard ref covers the pair — clicking the button blurs the input, which would
  otherwise fire two saves for one key.
- **It never tracks "did I just finish."** A successful save makes the project list non-empty
  and the view swaps itself; a failed one leaves the user on the last step with the error
  beneath it. Don't add local success state.
- **A provider with no credential-slot entry gets no key field at all** — which is what keeps
  the mapping from gating new providers, and what makes `mock` work keyless. If the deployment
  publishes no provider descriptors, the step is **dropped entirely** — two steps, not an empty
  third one.
- **E2e:** the keyless onboarding spec is the ONE spec that drives the flow end to end (it needs
  a mock GitHub adapter, which no headless test can complete against real GitHub). Every other
  spec seeds a project over `PUT /api/project` — **don't couple new specs to the flow.**

### `/signup` replays that flow on demand

`src/signup/` is a **second mount of the real flow, not a second flow**. It exists because the
team iterates on sign-up and the dashboard only shows the guided steps while the project list
is empty — the second look used to cost a fresh or wiped GitHub account. **Never fork the
components to make the rehearsal easier**; if you change the flow, you change what `/signup`
shows, and that is the whole design.

- **Reads are live, writes go nowhere.** This is not squeamishness: `saveProject` is an
  **upsert over the caller's FIRST project**, so a rehearsal that reached the network would
  rewrite the project the tester actually works in — and provider keys are user-scoped, so a
  throwaway key would replace the real one every project of theirs runs on. The suite asserts
  the whole flow finishes with every transport write un-called; **keep that case.**
- **Restart is a remount, never a reset method.** A reset routine would silently miss a field
  (the step index, a half-typed key, the simulated grant) the first time someone adds one.
- **GitHub's consent screen is the one thing faked**, via two optional props that are absent
  everywhere else and change no DOM when omitted.
- **The simulated store mirrors the real one's SEQUENCE, not just its results** — pending →
  save → chained verify → cleared — because the credential indicator going `…` then `✓` is part
  of the experience being rehearsed.

### Settings is the one desktop-first surface

`/dashboard`'s settings view is **not** mobile-first — it is a management page visited from a
laptop: a two-column shell (sticky section nav + section cards) with compact controls.

- **The nav scrolls to sections; it does NOT swap panes.** Every field stays mounted. That is
  what keeps find-in-page working, keeps a section deep link from hiding the rest of the page,
  and — load-bearing for the suite — keeps fields reachable on load instead of behind a tab a
  test would have to discover. Add a section to the one `SECTIONS` source of truth and keep the
  pane mounted.
- **Know which config component you're touching.** `CredentialFields` is Settings-only, so it
  is free to restructure. `ProjectFields` is shared by **three** surfaces — restyle it from
  `Dashboard.css` (scoped so the app-native projects page is untouched), **don't reshape its
  DOM**. Its `form` layout must not move; the `detail` layout is the project modal's shell, and
  both branches render the *same* field elements built once above the branch, so the two shells
  can't drift in what they render or submit.
- **Icons decorate, they never replace a label** here — hand-rolled inline SVGs, no icon
  library. They carry no `width`/`height`: **one rule sizes the whole tree in `em`**, and
  Onboarding needs its **own** copy of that rule because the settings one is scoped. Drop it and
  every glyph renders at the SVG default 300×150 — with every DOM test still green, since the
  elements are all present at the wrong size.
- **Selects are drawn by us, not the platform** — `appearance: none` plus a chevron made of two
  `currentColor` gradients (not an SVG data URI, which can't read a token and would hard-code a
  hex in one theme). Consequence: any later rule touching a select must use `background-color`,
  **never the `background` shorthand**, which resets the chevron.
- **The projects list is a panel per project over a detail modal** — the page's one deliberate
  exception to "the nav never swaps panes", because N inline project forms are what made the
  list unscannable.
- **The modal is hand-rolled, not `<dialog>`.** jsdom ships **no `HTMLDialogElement`**, so a
  native dialog would be untestable in the gate; the component owns Escape, the scrim press
  (only one that *starts* on the scrim dismisses), a Tab trap, scroll lock and focus itself.
  Vaul isn't the answer either — it is a bottom sheet for the mobile screen.
- **The store's project mutations resolve `boolean`**: they fold failures into `error` rather
  than rejecting, so the boolean is the only signal a caller has. The modal closes on `true` and
  stays open — with everything typed into it — on `false`. **Don't "simplify" these to
  `Promise<void>`.**
- The per-project sandbox catalog mounts **inside the open modal**, so only the project being
  looked at fetches its catalog. **Do not put snapshot state in the global dashboard store** —
  it can't serve N project cards.

### Creating a project asks for the REPO and takes the name from it

`project === undefined` is the create mode, and it **drops the name field entirely** — the
submitted `name` is derived from the repo URL by one helper (`dashboard/project-name.ts`: last
segment, `.git` and trailing `/` stripped). **Three surfaces read that one helper** — the
app-native create step, the settings modal's create mode, and Onboarding's step 2 — so a repo
cannot end up named three ways.

- **Editing is untouched, and nothing re-derives on save.** A board someone deliberately
  renamed must not snap back to its repo's name the next time an unrelated field is edited.
- **A note element replaced the field** (`project-name-note`, with the derived name inside).
  It is rendered by both `ProjectFields` layouts *and* hand-rolled in Onboarding — **without it
  the naming is invisible and a board appears called something nobody was shown. Don't drop
  it.**
- **The submit reads "Create project" in create mode**, "Save project" when editing; e2e and
  DOM tests bind to both.
- **The create arrangement demotes the defaulted fields, it does not drop them** — a
  multi-provider deployment still has to be able to name the agent at creation.

### `/projects` — the app-native page, and its full-screen create step

`projects/ProjectsManager.tsx` is the mobile, app-styled view over the same dashboard store.
The desk rail's "New" routes here with `?new=1`; the phone's project menu dropped its own
"Add", so on a phone the way in is this page's own affordance. The list is a column of
collapsible rows, and **creating is a step that takes the screen**, not a card at its foot.

- **`creating` lives in `ProjectsScreen`, not `ProjectsBody`**, because the page header
  renders from it: while the step is up the `h1` reads "New project" and the leading control
  becomes a cancel rather than the link to `/app`. It is gated on the body actually being on
  screen, so a `?new=1` arriving before `me` does still leaves a way back during the load.
- **The body returns the step INSTEAD of the list** — that is what "full-screen" means here;
  the rows and "Add project" are not under it.
- **The step's geometry is measured, not asserted in jsdom** (`tests/layout/other-shells.spec.ts`):
  the picker on screen without scrolling, spanning the column, at a real touch height, with
  the create action spanning the column at the foot. Every one of those passes as a DOM
  assertion while looking wrong — the shape it replaced was a name field over a picker in a
  card.
- **The layout harness stubs the repo listing as connected**, because a disconnected picker is
  a paragraph of prose rather than the control being measured.

## The desktop shell and `/kanban`

`/app` answers "what should I look at now" (the brain's curated feed, 08 D1); `/kanban` answers
"where does everything stand", which the feed structurally cannot — a ticket can sit in Ready
for a day without earning a card.

- **`useIsDesktop()` is the only breakpoint.** The CSS deliberately carries **no** `min-width`
  media query for the shell — a second threshold could silently disagree with the JS one.
  Asserted in `stylesheet-discipline.test.ts`.
- **`DesktopScreen.css` layers on top of `PrimaryScreen.css`.** The feed card, divider, "show
  earlier", mic and send/clear are styled by *unscoped* rules in the mobile sheet, so the desk
  inherits one visual language for free and states only what a desk earns. **Don't scope those
  mobile rules** — the desktop shell depends on them being global.
- **The kanban root wears the desktop shell's role with `data-view='kanban'`**, rather than a
  role of its own — that is what makes it the same shell (viewport lock, box model,
  visually-hidden helper, the bell's re-anchoring) for free, overriding exactly one declaration.
  A second root role would have meant restating four rules that must never disagree.
- **`useDesktopShellFlag()` publishes `<body data-shell="desktop">` and BOTH desktop shells must
  call it.** It is how the ticket sheet — which portals to `document.body` — learns it is a
  right-edge panel and not a full-bleed phone sheet. A shell that forgot it would open its panel
  as a bottom sheet across a 1440px window, **and nothing in the DOM gate could see that.**
- **The accent budget is spent exactly once per sheet**, and a test asserts it: the rail's
  `needs-you` dot on the desktop shell, the blocked edge on kanban. That is not a second budget
  — it is the same rule ("a person is needed for a decision") applied to the one thing on each
  board that qualifies. A window left open all day must not carry a permanently lit accent.
- **The desktop shell pins no theme — it follows the OS like every other route** (13 D6a). It
  used to stamp `data-theme="dark"` on `<body>`, beating the system preference and giving one
  user paper on their phone and near-black at their desk. There is exactly one theme mechanism.
  **Don't reintroduce a per-surface override**; a test asserts the sheet names no theme.
- **Every desktop rule has to hold in both registers**, and the trap is picking the token that
  *looks* right in whichever theme you have open: `--surface-raised` is a lift above
  `--surface-card` in dark but three hex points off it in light, so a "firms on hover" written
  as `raised` becomes "vanishes on hover" in daylight. **Reach for the token that carries the
  intent.**
- **Ticket detail is a RIGHT-SIDE panel at a desk, a bottom sheet on a phone** (13 D7a), and the
  split is one prop plus one body attribute. The prop is handed straight to vaul as its
  `direction`, which derives the entrance, the closed transform *and* the drag axis — so the
  edge is stated **exactly once**. **Never re-state a slide in CSS to move the sheet**: vaul
  writes those as *inline* transforms, so a CSS `transform` is ignored and, forced with
  `!important`, strands the panel permanently open.
- **Columns are the five states in pipeline order**, deliberately NOT the mobile board's three
  zones. **Ticket order inside a column is the server's, untouched** — `ready` arrives in exact
  pull order (03 §5), so re-sorting locally would destroy the one column whose order carries
  information.
- **The working strip and a kanban card's status mark read the BOARD, not the feed** — the feed
  is the brain's curated narration (08 D1) and a ticket can be worked for an hour without
  earning a card. A status mark is `null` for a ticket with no worker bound: a Ready ticket
  painting a session dot would be inventing a session. **Working rows sort by
  `state_changed_at` ASCENDING**, so a ticket picked up now appends at the bottom and nothing
  on screen moves; newest-first would reflow the strip under the eye.
- **The tickets column's width belongs to the USER, and it is published imperatively**
  (`use-working-panel-width.ts`, whose header carries the full rationale). The boundary
  between that column and the feed is a draggable separator; the shipped 248px is the
  **floor**, not the whole story, and the ceiling is twice it. Four things to keep:
  - **The live width is a ref written straight to the DOM** — a custom property on the shell
    root and the separator's `aria-valuenow` — **never React state.** A drag fires a move per
    frame, and re-rendering the shell (the whole feed card list with it) on each one is how a
    handle comes to feel like it drags through treacle. Both targets are single-writer, so a
    re-render behind a drag cannot fight it. What *is* state is `dragging`, which flips twice
    a drag and publishes `data-resizing` for the window-wide cursor and selection lock.
  - **The rule IS the handle** — the 1px line moved off the panel's `border-right` onto the
    separator's `border-left`, so the boundary sits at the same x and the resting screen gains
    nothing to look at. The grab room reaches **left only**, because the feed's region paints
    after the separator and would win the hit test on its right.
  - **It is the ARIA window splitter**, so the keyboard path mirrors the pointer's: arrows
    step, Home/End go to the ends. **Home is also the reset** — the default width *is* the
    minimum — so don't add a double-click or a menu item for it.
  - **The bounds are literals in three places** (the hook, the CSS fallback, the layout spec)
    and that is deliberate: `tests/layout/desktop-shell.spec.ts` measures the resting width,
    the drag and both clamps in a real browser, which is the only thing that can catch the
    three disagreeing.
- **Cross-project rail status is a poll, and that is deliberate.** There is no server-side
  cross-project status endpoint, and 13 §11 scopes desktop as frontend-only over existing
  contracts, so the hook reads each *non-selected* project's board on a slow interval. **If
  projects-per-user grows past a handful, add one server status endpoint — don't tighten the
  interval.**
- **Module-level caches bridge the project-switch remount, and they are load-bearing.** The
  subtree is keyed by project id so a switch tears the stores down and re-opens the stream (12
  §4.1) — so seeding from empty would blank the rail and flash a loading feed at exactly the
  moment the user is looking. Three rules: the cache **never replaces the fetch**; the feed
  caches its **optimistic suppressions** too, or a card the user just dealt with flashes back;
  and the feed **restores** the frozen last-seen divider rather than re-freezing it.
- **`loading` stays true through the refresh that runs behind cache-seeded data** — that is the
  point, not an edge case. The desk renders it as one faint line and **withholds "All quiet."
  while it is up**: the resting line asserts we asked and there was nothing, and saying it
  mid-fetch teaches the user not to believe the line the screen most needs believed.
- **Deliberately NOT ported to the desk** (all spec calls, not omissions): swipe / per-card
  dismiss and bulk clear (the brain curates, 08 D1), pull-to-refresh (a touch gesture), and the
  header's ticket dropdown. **Don't "restore parity" without reopening those questions.**
- **The desk shell is ALSO a touch shell, and a short feed has to bounce.** The breakpoint is
  width-only, so a landscape tablet gets this layout driven by a finger — and a scroller whose
  content fits has nothing to scroll and **no rubber-band to give**. One wrapper holds
  everything the region scrolls, held one pixel past the scrollport under coarse pointer. That
  pixel is a *term* in the wrapper's `min-height`, not the whole of it. Coarse pointer only —
  with a mouse the pixel buys a permanent scrollbar. And **never pair it with
  `overscroll-behavior` on the feed**: iOS WebKit reads `contain`/`none` as "no elastic bounce
  either" and would suppress exactly what the pixel unlocks.
- **The desktop composer does not use the voice store's `keyboardMode`** — that toggle is modal
  (entering stops the mic) because a phone has room for one input at a time; a desk doesn't, so
  the field and the mic coexist and the user picks per-utterance.
- **`DesktopComposer` holds NO text state — the field IS the store's transcript.** It used to
  keep a local draft and *cancel* the armed auto-send on focus, which is exactly what made a
  correction cost the user their send. **Don't reintroduce a draft copy**: one buffer is what
  lets the countdown pause and resume instead.
- **Copy that differs by shell is a prop, not a fork** — the feed card takes a `moreLabel`
  defaulting to the mobile wording. Only the text node changes, so every mobile snapshot stays
  byte-identical. **Don't fork the component.**
- **A shared popover inherits its ANCHOR too, and the anchor is a fact about the shell.** A
  dropdown's `top`/`right` encode where its trigger sits, and the two shells put the same
  trigger in opposite corners — the mobile anchoring aimed the bell off the bottom *and* off the
  left of a `100dvh` shell that cannot scroll it back, i.e. invisible, not merely misplaced.
  Two things to carry to the next one: **release BOTH mobile anchors** or the panel stretches
  between them instead of moving, and **re-state the open-state `transform` at higher
  specificity**, or the closed transform wins and the panel opens stuck out of place. Scope the
  override rather than editing the mobile rule — a resize swaps shells with both stylesheets
  still loaded.
- **Sharing a rule is not sharing a clock.** Marks that must pulse together also need the
  shared-phase opt-in: a CSS animation's timeline starts when **its own element** starts
  animating, so one shared declaration still runs from a different start per element. This
  column was fixed three times — matching the tempo, then sharing the element's rule — and they
  *still* drifted; identical marks peaking apart is the worst of the readings, because the eye
  tracks the shimmer between them instead of either mark. `src/pulse-phase.ts` pins every
  opted-in animation's `startTime` to the document timeline. **The opt-in is a CSS custom
  property on the rule that declares the `animation`**, not a list of keyframe names in the
  module — the rail's project dot breathes at its own slower tempo, alone in a column with
  nothing to keep time against, and a name list could not tell it apart from a mark that wants
  the sync. **When you unify two animated marks, check the phase as well as the look** — a
  CSS-string test proves they share a declaration and tells you nothing about whether they peak
  together. `tests/layout/status-mark.spec.ts` reads every breathing mark's live animation in a
  single `evaluate` and asserts one `startTime` and one `currentTime` across all of them.
- **A glow is geometry too — an opaque band anchored to a region's edge will cut it.** The
  listening mic radiates a ring ~20px past the button's edge, and the activity row is anchored
  to the composer region's top edge carrying an opaque fill. The composer sat flush against
  that edge, so every toast sliced the ring off along a hard horizontal line — invisible to
  every DOM test, because a box-shadow occupies no layout box. Fix is clearance on the
  containing block, **never a z-index that lifts the mic over the band** (that just moves the
  collision and paints the halo across the pills). **When you anchor anything to the edge of a
  region holding the mic, budget the glow's reach, not the button's box.**

## Potential gotchas

Mostly jsdom and vaul gaps — traps you meet in tests, not in the code.

- **A wrapping `<label>` absorbs everything inside it into the field's accessible name.** The
  credential inputs each sit beside a live validity glyph; while the glyph was inside the label,
  `getByLabel('Amika API key')` worked right up until the first ✓ rendered, then silently
  stopped matching — in Vitest *and* Playwright. Wire the label with `htmlFor`/`id` and keep
  only the label text inside it. Same trap as the onboarding radios' hint text.
- **jsdom does no layout**, so a clamp, an overflow measurement or a sticky offset simply isn't
  observable there. Fake the heights where a component measures its own clamp; put the real
  claim in `tests/layout/`.
- **jsdom ships no `PointerEvent`** — `new PointerEvent(...)` throws, and testing-library's
  `fireEvent.pointer*` silently drops `clientX/clientY`, so a gesture reading coordinates sees
  `NaN`. Polyfill with a `MouseEvent` subclass (jsdom carries mouse coords), and guard
  `setPointerCapture` with a `typeof` check.
- **jsdom ships no `IntersectionObserver`** — the settings scroll-spy guards on it and degrades
  to a static highlight. To test the observing path, capture the fake's callback on a **holder
  object**, not a bare `let`: TypeScript keeps the `null`-narrowing of a local across an
  assignment made inside a nested class constructor.
- **jsdom has no `HTMLDialogElement`** and no `scrollIntoView`, `AnimationEvent` or
  `getAnimations`.
- **Sending pointer events into the vaul sheet hits two jsdom gaps in vaul itself**, surfacing
  as uncaught `TypeError`s that fail the run even when every test passes (vitest exits 1 on
  unhandled errors). The events reach vaul's drag handlers above whatever you aimed at, as they
  do in the browser; it then calls `setPointerCapture` and reads a transform chain that lands on
  `undefined`. **Stub those in the test file rather than making the component swallow events to
  stay testable.**
- **The vaul sheet is a Radix Dialog underneath.** `data-state` is Radix's, so the ticket's own
  lifecycle state rides on `data-ticket-state`. Content **portals to `document.body`**, so the
  skin can't key off DOM ancestry and tests must query via `screen`/`document`, not the render
  container. **The accessible name is the `<Drawer.Title>`** — Radix wires `aria-labelledby` to
  it, which per accname *beats* any `aria-label` you also pass. Dismissal (Escape, scrim, drag)
  is vaul's, surfaced as `onOpenChange(false)`; don't hand-roll it, and don't assert on the drag
  physics, which don't work in jsdom.
- **The sheet header carries no × — don't add one back.** Those three dismissal paths are the
  whole of it, and a button was a fourth; it cost a chrome column down the right edge that the
  title had to shrink to clear. Tests press Escape instead.
- **A touch device *emulates* hover, so `:hover` is not a pointer-only rule.** A finger that
  merely starts a scroll on an element with a `:hover` background paints it on the way past —
  the element reads as pressed when nothing was pressed. Any press feedback inside a scroll
  region belongs behind `@media (hover: hover)`, with touch's half driven from the component.
  Two rules for that half, both learned the hard way: **it cannot go on at `pointerdown`** (a
  scroll starts with one too — wait a beat), and **it must come off on `pointercancel`**, which
  is how the browser ends a touch that turned into a scroll.
- **A `max-height` flex column shrinks EVERY item, not just the scrolling one.** Flexbox removes
  overflow in proportion to (shrink factor × flex base size), so with the default factor the
  capped ticket sheet's dock lost height to a long body and the live transcript clipped its own
  text behind an invisible scroll (touch draws no scrollbar). The fix is shrink **priority**,
  not a bigger cap: the scrolling body yields first, while the dock keeps a last-resort valve so
  its controls can't be clipped off a very short viewport. **Same trap applies to any other
  capped sheet.**
- **Viewport units: match the unit the container already uses.** On mobile Safari `vh` is the
  *large* viewport, so a `45vh` child inside an `85dvh` parent can claim more than it looks like.
- **A headless screenshot of the tickets dropdown comes out blank.** `[data-role=
  'header-status-list']` is `overflow-y: scroll`, i.e. its own scrolling layer, and in the
  layout harness's headless Chromium it captures empty in both page and element shots — while
  the rows have real boxes, real ink and answer `elementFromPoint`. It is a **capture
  artifact, not a UI bug**: check geometry with `box()`/`computed()` first, and if you
  genuinely need the pixels, add a throwaway `page.addStyleTag` overriding the overflow before
  the shot. **Don't "fix" the stylesheet for it.**
