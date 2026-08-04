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

**Two things are deliberately not decided here.** Whether this ships as an installable
desktop app or as the responsive web app is an implementation question for later — nothing
below depends on the answer. And no implementation tickets are scoped here; that comes
after this document is agreed.

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
