# Kiln for Desktop — the feed-based experience

**Date:** 2026-08-04
**Status:** Draft / design — for discussion
**Scope:** The desktop experience of the Kiln app. Design only; no implementation.
**Relationship to `01`–`12`:** `08` decided the *mobile* primary screen — a feed over a live
board, with a voice dock. This document asks what that same product is on a desktop
screen, and answers it as **an ambient window you leave open**, not as a denser version of
the phone. It reuses `08`'s feed model and `12`'s project scoping. It does **not** revisit
the board mechanics (`03`), the brain (`06`), or the wire-contract discipline (`02` §3–§4).

---

## 0. What this document is, and what it is not

**It is a description of an experience.** It exists to settle what Kiln on a desktop
*feels* like, so that later tickets have something to be shaped from. It leads with the
feeling on purpose: every structural question below — what goes in which region, what a
row says, what happens on hover — is downstream of a single idea, and getting the idea
right matters more than getting the panel widths right.

**It is not the ticket board coming back.** An earlier direction imagined desktop as the
place where the Kanban board finally gets the room it never had on a phone — columns,
drag, WIP limits made visible. That direction is **dropped entirely**. `08` retired the
board as the product surface for a reason that does not change when the screen gets
bigger: the board is *mechanism*, and mechanism is what Kiln exists to handle for you. A
board asks you to manage. This asks you to glance.

**One thing is deliberately not scoped here.** No implementation tickets are written; that
comes after this document is agreed. The **delivery form is not an open question**: this is
the responsive web app widening into a desktop layout, not a separate installable
application (D8).

---

## 1. The feeling

Kiln is **ambient**. It is a thing that is on.

The nearest honest description is what it is like to **use an app while it is being
built** — not to watch a build, to *use* one. Things are in motion underneath you. A
change lands and the thing you were looking at is a little different than it was a minute
ago. Nobody interrupted you to tell you. It is the feeling of a `git pull` landing while
you work: the state moved, quietly, and it was fine.

That is the register. Kiln is **listening, working, present without being loud.** Most of
the time there is nothing you need to do, and the app should be honest about that rather
than manufacturing activity to justify its window. It should be **simple, quiet, and kind
of invisible until it needs you** — and when it does need you, that should be the one
loud thing on the screen.

This is the same underlying idea as the mobile app's *Kiln listening in the background*
feel. On a phone that feel is carried by absence: you close the app, it keeps going, a
notification brings you back. On a desktop the app is not closed — it is a window you left
open on a second monitor, or a tab you have not touched in an hour. So the feel has to be
carried by **presence** instead: it is there, it is showing you that things are moving,
and it is not asking for anything. Same idea, opposite mechanic.

A few things follow directly from that, and they are the whole design:

- **The resting state is the real state.** Nothing needing you is not an empty state to be
  apologized for. It is the state Kiln is trying to keep you in, and it should look
  composed — the app at rest, not the app with nothing in it.
- **Change arrives; it does not announce.** New things appear. They do not slide, bounce,
  badge, or steal focus. If you happen to be looking, you see it land. If you are not, it
  will still be there.
- **One exception, and only one.** When Kiln genuinely needs a decision, it stops being
  quiet. That contrast only works because everything else stayed quiet.
- **It should be pleasant to leave open.** If a window is going to sit in your peripheral
  vision all day, it cannot flicker, it cannot be bright, and it cannot be busy.

---

## 2. What it is like to use

**You open it in the morning.** A dark window. Down the left, your projects — a short
list, each one a name and a small sign of life. One of them has a dot lit; something over
there wants you. The rest are just... fine. On the right, the project you were last in,
and a column of what has happened: three things finished overnight, one thing is being
worked on right now. You read four lines and you know where everything stands. Then you
go do something else.

**You leave it open.** An hour later you glance over. There is a new line at the top that
was not there before. It did not ask for you; it just arrived, the way the file you have
open changes under you when a pull lands. You read it, you don't act on it, you look away.
This is the majority of the experience, and it is supposed to be.

**Something needs you.** The project in the rail lights up. The window does not jump — you
notice it because it is the only thing on screen with any heat in it. You click over. At
the top of the feed is the question, in full, in plain language: the agent hit a fork and
wants a call. You answer it right there, in your own words, the way you would answer a
colleague — and the moment you do, it stops being the loud thing. The rail goes quiet
again. Work resumes without you watching it resume.

**You have an idea.** You don't file a ticket. You say what you want, in a sentence, and
Kiln takes it from there — it comes back shaped, as something you can look at and agree
to. You agree, or you say what is wrong with it. There is no form. There is no board to
put it on.

**You switch projects.** The rail is the switch. The feed on the right becomes that
project's feed. Nothing was paused while you were away — the other projects kept running,
and their part of the rail kept quietly telling you so.

**You close the laptop.** It keeps going. That is the point. When it needs you next, it
finds you.

---

## 3. The shape on screen

Two regions, split left and right.

```
┌────────────────┬─────────────────────────────────────────────┐
│                │                                             │
│   PROJECTS     │   FEED                                      │
│                │                                             │
│   ● kiln       │   what this project is doing right now,     │
│     atlas      │   and what has happened lately —            │
│     ledger     │   newest at the top                         │
│                │                                             │
│   + new        │                                             │
│                ├─────────────────────────────────────────────┤
│                │   say something                             │
└────────────────┴─────────────────────────────────────────────┘
```

**Left — the projects rail.** The list of projects you own, and the way you move between
them. It does double duty: it is the switcher, *and* it is the ambient layer. Each project
carries its own quiet indication of state, so the rail alone answers "is anything wrong
anywhere" without you opening anything. This is where the peripheral-vision job lives —
it is the part of the window you are not really looking at, and it is designed for not
really looking at it.

**Right — the feed.** Where the selected project currently stands. One column, newest
first: what needs you, what is proposed, what has happened. This is `08`'s feed, given
room. The room is not spent on more columns — it is spent on **air**, on letting a line of
text be a readable line of text rather than a truncated one.

**Below the feed — the way you talk to it.** Kiln has always been conversational; that
does not change on desktop, only the default input does. On a phone you talk. At a desk
your hands are already on a keyboard, so **typing is the primary input on desktop** and
voice is there when you want it. It is a single place to say something, not a form.

That is the whole layout. There is no third pane, no inspector, no bottom drawer of logs.
Detail opens over the top of the feed when you ask for it and gets out of the way when you
are done. If a future need seems to want a third region, it is worth asking whether it
actually wants `/debug` (`08` §6), which already exists and is where raw state belongs.

---

## 4. How it should look

**Dark, near-black, understated.** Desktop rests in the dark register. Kiln's dark theme
is a *warm* near-black — a charcoal with brown in it, never a blue-black — and that stays;
"black" here means the warm one the product already has (`tokens.css`), not a new colder
one. A window you keep open all day should recede into the desk, not glow at you.

**Minimal to the point of being nearly invisible.** Very little chrome. Borders are
hairlines or nothing at all; separation comes from space more often than from lines. No
panel headers announcing what a panel obviously is. The window should look, at a glance,
almost empty — and be dense with meaning when you actually read it.

**Color is a signal, not a decoration.** The palette is near-monochrome by default, and
the accent — Kiln's fire — appears only where something needs a person. If the accent is
on screen, it means something. That is the entire contrast budget, and spending it on
anything else breaks the one loud thing that is supposed to work.

**Rounded, soft-edged, humanist.** Rounded corners on cards and controls, generous radii,
pill-shaped controls — the same physical vocabulary as mobile. It keeps the app feeling
like a calm object rather than a dashboard.

**The same design language as mobile, not the same design.** Same tokens, same type
families, same color meanings, same motion character — settle, don't bounce. What changes
is what a desktop earns: **tighter density** (a desk is read at a different distance than
a phone held up, and touch targets sized for thumbs waste a desk's space), and **input
affordances a phone doesn't have** — hover as a way to reveal detail without committing to
it, and the keyboard as a real way to move around. Both of those are how the ambient
quality survives: hover lets a row stay minimal until you point at it, and keyboard
navigation means the app can be used without ever becoming a thing you click through.

**Motion is minimal and unhurried.** Things fade and settle. Nothing slides in from an
edge to catch your eye. The one thing that is allowed to move on its own is the sign that
Kiln is working — and it should read as breathing, not as loading.

---

## 5. The projects rail

The rail is the left column, and it is doing two jobs at once. Keeping them in one place is
a deliberate choice (D3): a separate "status of everything" surface would be a second thing
to look at, and the whole premise is that there is one thing to look at and mostly you
don't.

**As a switcher**, it is the desktop form of `12` §4.1's project switcher. Selecting a
project re-scopes the feed and the stream to it, exactly as the mobile switcher does today,
keyed on `project_id` (`12` DP5). The current selection persists across sessions.

**As the ambient layer**, each row carries a compact, glanceable state for that project.
The rail is read peripherally, so a row's state has to survive being seen out of the corner
of an eye. That argues for a very small vocabulary of states — on the order of *needs you*
/ *working* / *quiet* — expressed primarily as presence-or-absence and only secondarily as
text. The exact vocabulary is a design question worth resolving against real projects
rather than in the abstract (§13), but the constraint is firm: **only "needs you" gets the
accent.** If two states both draw the eye, neither does.

Ordering should be stable — a rail that reshuffles itself is the opposite of ambient. Most
recently used or created-at order (`12` §4.1) both qualify; what does not qualify is
sorting by urgency, which would move rows under the pointer exactly when they matter.

Below the list sits the way to add a project, routing to the app-native project-management
page as it does today. Managing projects stays on `/dashboard` and `/projects`; the rail
switches between them, it does not administer them.

## 6. The feed

The right column is `08`'s feed, unchanged in model. The card kinds and their sourcing are
exactly `08` §3 — **blockers** derived from Blocked tickets, **proposals** derived from
Shaping tickets, **updates** authored by the brain — as is the ordering (blockers pinned,
then proposals, then updates newest-first) and the retained-history-with-a-last-seen-divider
lifecycle (`08` D2′). Nothing about the feed's *truth* changes because the screen is wider.
What changes is what the screen can afford to show of it.

- **Room goes to legibility, not to more.** A wider column means a title that doesn't
  truncate and a blocker question that reads as a paragraph rather than a clipped line. It
  does not mean a second column of cards, a grid, or a sidebar of metadata.
- **A row is minimal at rest and fuller on hover.** This is the desktop affordance that
  most directly serves the ambient goal: secondary detail (timestamps in full, the actions
  a card supports) can stay off the screen until the pointer is on the row. A card that is
  quiet until you point at it is a card you can leave in your peripheral vision.
- **Detail opens over the feed, not beside it** (D7). Opening a ticket brings up its detail
  and transcript over the top of the column; dismissing returns you exactly where you were.
  A permanent detail pane would double the screen's resting complexity to serve something
  you look at rarely, and would quietly re-create the two-pane management console this
  design is trying not to be.
- **Arrivals land in place.** A new card appears at the top of the updates section without
  scrolling the column under you, without a badge, and without motion beyond a fade. If you
  were reading something, you keep reading it. This is the "git pull landed" property, and
  it is the single most important behavioral detail in the document.
- **History is scrollable and paged**, per `08` §3 — the divider still means *what was new
  when you arrived*, frozen for the session. On a desk you scroll back further and more
  often than on a phone, so the "show earlier" path should feel like a normal scroll rather
  than a deliberate act.
- **Swipe has no desktop equivalent, and needs none.** The mobile swipe-to-dismiss gesture
  does not become a hover-revealed close button by default; whether any card is dismissible
  by hand on desktop is a genuine open question (§13), not an automatic port.

## 7. Saying something

Talking is still the interface for everything that isn't mechanical (`08` §5). On desktop
the *default modality* flips (D5): **typing is primary, voice is secondary.** At a desk the
keyboard is already under your hands, typing is silent in a room with other people in it,
and a typed sentence is easier to get right than a spoken one when it contains a filename.
Voice remains available — it is the same message seam either way (`09`), and the brain
cannot tell the difference — but it is not the thing the layout is built around.

Concretely, that means the desktop's input is **one quiet line under the feed**, always
there, focusable from anywhere by keyboard, with the mic as an affordance on it rather than
as the centerpiece the mobile dock makes it. It is not a form and it never becomes one:
there is no ticket-creation dialog, no title field, no priority select. You say what you
want and Kiln shapes it into something you can agree to.

Kiln's replies (`08` §4's `say`) and its action toasts belong in the same low-key register
they have on mobile — near the input, transient for toasts, persistent for a reply until
you move on. The **acceptance gate is unchanged**: a proposal card carries an Accept
affordance that is a mechanical `MarkReady` (`08` §5, D6), and declining or amending is
something you say, not something you click.

## 8. The ambient layer

"Things are in motion" has to be *visible* or the premise fails — but visible is not the
same as attention-grabbing. Three signals carry it, and between them they should be enough
that a glance answers "is it alive, and does it want me":

1. **The rail's per-project state** (§5) — the peripheral one. Answers "is anything wrong
   anywhere" without reading a word.
2. **The working indication** — Kiln is thinking or agents are mid-turn. This is `08` §4's
   `thinking`, and it is the one element permitted to animate on its own. It should read as
   breathing: slow, low-contrast, never a progress bar (there is no progress to report, and
   a bar that doesn't measure anything is a lie).
3. **The feed itself changing** — the strongest signal, and free. Cards arriving and
   blockers clearing *are* the evidence that work is happening. The design's job is mostly
   to not get in their way.

What is deliberately absent: unread counts, notification badges on the window, a live log
tail, per-agent progress meters, and anything that ticks. Each of them would convert
"present" into "demanding," which is the failure mode this whole document is written
against.

## 9. Interaction model

- **Hover reveals; it never acts.** Pointing at something can show more of it. Pointing at
  something never changes state, never opens anything, and never starts a timer that will
  open something.
- **The keyboard is a first-class way through the app**, not an accessibility afterthought.
  At minimum that means moving between projects in the rail, moving between cards in the
  feed, opening and dismissing detail, jumping to the input from anywhere, and getting back
  out of it. Concrete bindings are a later decision; the commitment here is that a full
  pass — glance, read a blocker, answer it — should be possible without the mouse.
- **Focus is visible and follows the same rules as everything else** — the existing focus
  ring token, not a new invention.
- **Nothing is drag-and-drop.** There is no arrangement for the user to author. This is the
  clearest concrete consequence of not being a board.
- **Density is desktop density.** Touch targets sized for thumbs are wasted at a desk;
  type ramps and spacing tighten the way `/dashboard`'s settings surface already does (the
  one existing desktop-first surface in the product, and the right precedent to follow).

## 10. The states it has to hold

| State | What it should feel like |
| --- | --- |
| **Resting** — nothing needs you | Composed, not empty. The rail is quiet, the feed shows recent history, the window looks almost blank and is completely honest. This is the state the design is optimized for. |
| **Working** — agents mid-turn, nothing needed | The breathing indication is on; the feed is otherwise still. "It's handling it." |
| **Needs you** — a blocker or a proposal | The one loud moment. Accent present, question in full, answerable in place. Everything else on screen stays exactly as quiet as it was. |
| **Fresh / zero projects** | Falls through to the existing setup gate (`12` §4.1). Desktop does not invent a second onboarding. |
| **Disconnected** | Must be stated, not hidden — an ambient app that has silently stopped receiving is worse than one that is visibly off. Low-key and permanent while it lasts, not a modal. |

## 11. The seams this touches

Not a build plan — a map, so a later scoping pass knows where it is working.

- **Frontend only, in the main.** The feed model, the card kinds, the seen/divider
  semantics, and project scoping all exist server-side already (`08` §7, `12` §3). This is
  overwhelmingly a client-side design landing on top of contracts that are already there.
- **The mobile screen is not being replaced.** Mobile-first remains the product's stance
  (`02` §11); this is an additional expression of the same app — the same client, widened —
  and exactly how the two shells share that client is left open (§13 Q4).
- **`/debug` stays where it is** (`08` §6). Every "shouldn't desktop show raw state?"
  impulse has an existing home, and letting it into the primary screen is how the ambient
  quality dies.
- **Notifications keep working the way they do** (`10`, `12` §6.3) — including landing on
  the right project. Desktop being open does not remove the need to be found when it isn't.

## 12. Deliberately not decided here

- **Implementation tickets.** None are scoped. This document is what they get shaped from.
- **Exact metrics.** Column widths, breakpoints, the type ramp's desktop values, and
  specific key bindings are all downstream of agreeing the experience.

## 13. Open questions

1. **Does the feed ever go cross-project?** The reading taken here is that "where each
   project stands" is answered by the **rail** (per-project state, always visible) while the
   **feed** shows the selected project's activity — the pair covers it, and it matches how
   scoping already works (`12` §3.2). The alternative is a genuine "All projects" feed with
   cross-project rows. **Recommendation: start with the rail-plus-selected-feed pair**, and
   let a cross-project view earn itself once there are enough projects to need it. Worth an
   explicit call before shaping tickets.
2. **The rail's state vocabulary** — how many states, and how much text each row carries
   (name only? name plus a phrase?). Resolve against real projects.
3. **Is any card dismissible by hand on desktop?** (§6). Mobile has swipe; desktop has no
   automatic equivalent, and the curated-feed model (`08` D1) argues the brain should be
   removing things, not the user.
4. **How mobile and desktop share one client.** One responsive tree, two shells over shared
   stores, or something else — an implementation question, but one with enough design
   consequence to name.
5. **What "kind of invisible" means for the window at rest** — whether there is anything
   between "open and quiet" and "closed" (a compact state, a minimal mode). Only worth
   asking once the resting state exists to look at.

## 14. Decision log

| # | Decision | Alternatives considered | Rationale |
| --- | --- | --- | --- |
| D1 | **Desktop is ambient-first** — a window you leave open that stays quiet until it needs you — not a denser rendering of the mobile screen. | Straight responsive scaling of `08`; a "power user" console with more surfaced at once. | The mobile feel ("Kiln listening in the background") is carried by absence, which a always-open window can't reproduce; presence has to carry it instead. Scaling `08` up would fill the space with density nobody asked for. |
| D2 | **No Kanban/ticket board.** The board is not the product surface at any screen size. | Revive the board on desktop where columns finally fit; a hybrid feed-plus-board. | `08` retired the board because it is mechanism, and mechanism is what Kiln handles *for* you. A bigger screen changes the space available, not the argument. A board asks you to manage; this asks you to glance. |
| D3 | **The rail is both the switcher and the ambient status layer.** | A switcher plus a separate cross-project status strip/header. | Two surfaces means two things to look at, against a premise of one thing you mostly don't look at. The rail is already in peripheral vision; giving it the status job costs no new screen real estate. |
| D4 | **The feed is scoped to the selected project; cross-project standing lives in the rail.** | A single cross-project feed; a feed with a project column. | Matches existing scoping (`12` §3.2) with no new server work, and keeps a blocker's context unambiguous. Flagged as §13 Q1 for an explicit call. |
| D5 | **Typing is the primary input on desktop; voice is secondary but present.** | Voice-first, mirroring mobile's dock; voice-only. | At a desk the keyboard is already under your hands, typing is silent around other people, and typed text is more precise for filenames and identifiers. Same message seam either way (`09`), so nothing downstream cares. |
| D6 | **Dark is desktop's resting register**, in Kiln's existing warm near-black — never a colder black. | Light-first to match mobile's paper default; a new desktop-only dark palette. | A window open all day should recede. Reusing the existing dark theme keeps one design language rather than forking the palette. |
| D7 | **Detail opens over the feed; there is no third pane.** | A persistent detail/inspector pane; a three-column layout. | A permanent pane doubles the resting complexity to serve something looked at rarely, and re-creates the two-pane management console this design avoids. Overlay keeps the resting state at two regions. |
| D8 | **Desktop is the responsive web app widening out, not a separate installable application.** | An installable/packaged desktop app; leaving the question open until later. | Directed, and it costs the design nothing: every behavior above works in a browser window, and the client holds no authoritative state (`02` §11), so "a window you leave open" is already just a tab you leave open. Packaging, auto-update, and window-chrome work would buy no part of the experience described here. |
