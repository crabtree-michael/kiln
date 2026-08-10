# Investigation — why one user reports not seeing the 👉 Poke

**Date:** 2026-08-10 · **Question:** a user reports the "👉 Poke" affordance on the idle ticket
view is missing for him but present for others. Is that a stale build, a per-user/per-org feature
flag, or something about how his account is set up? · **Code investigated:** `35b2d3e`

Investigation only — no product code changed here. **Answer: none of the three. Nothing in Kiln
can show or hide this affordance per user, and the "👉 Poke" that was reported missing has not
existed since the day after it shipped.** The affordance is now an icon-only 👉, gated on ticket
state alone. One loose end came out of the trace: no emoji anywhere in the client has a font
fallback, which is the one way a user really can see nothing where the 👉 should be (§5).

## 1. What shipped, and what replaced it

| commit | date | change |
| --- | --- | --- |
| `6284b5f` (#98) | 2026-07-11 | Replaced the play-icon "Poke to continue" with 👉 + the word "Poke". Gated to *blocked*, or *working* once the bound agent's session read `idle`. |
| `30c4fca` | 2026-07-12 | **Dropped the label.** Poke becomes icon-only to match its sibling Delete; the glyph is `aria-hidden` and the accessible name moves to `aria-label="Poke"`. |
| `ff5f0ed` | 2026-08-06 | **Removed the idle gate.** `building` is the normal status mid-turn, so the gate hid the button through most of the life of the tickets a user most wants to nudge. The `agentIdle` prop is gone. |

Current render — `frontend/src/components/TicketDetail.tsx:1036-1055`:

```tsx
{(isBlocked || isWorking) && onPoke !== undefined && (
  <button type="button" data-role="detail-poke" aria-label="Poke" …>
    <span data-role="detail-poke-emoji" aria-hidden="true">👉</span>
  </button>
)}
```

So the visible content is one emoji on a 54px disc. The string "👉 Poke" is not in the product,
and there is no longer an "idle ticket view" as a distinct thing: the gate is
`onPoke !== undefined && (isBlocked || isWorking)` (`TicketDetail.tsx:472`), with no reading of
session status at all. `sandboxStatus` survives in both shells but feeds only the gear menu's
sandbox line (`TicketDetail.tsx:165-172`).

## 2. It cannot vary per user — three checks

**No feature flags.** There is no flag system in the repo: no rollout gating, no per-user or
per-project UI toggles, nothing keyed on identity in any component. The affordance is wired
identically everywhere it appears — `useTicketActions` always returns a defined `onPoke`
(`frontend/src/components/ticket-intents.ts:106-115,182-193`), and every container passes it
through: mobile and desktop from `PrimaryScreen.tsx:152,193`, `/kanban` from
`KanbanScreen.tsx:101`. Only a deliberately read-only sheet omits it.

**No Kiln orgs.** Kiln's tenancy unit is the *project*, and it is strictly owner-scoped: every
project read filters on `owner_user_id` (`backend/internal/identity/postgres/store.go:271,313,354,400`).
There is no membership or sharing table, so two Kiln users cannot share a board. The only
sharing-shaped mechanism in the code is boot-time adoption, which folds orphan rows into
`KILN_BOOTSTRAP_GITHUB_USER`'s single project (`backend/cmd/kiln/bootstrap.go`) — that moves data
ownership and never touches rendering.

**One build, for everyone.** Production is a single Render web service with the frontend embedded
in the Go binary, auto-deploying from `main` (`render.yaml`). There is exactly one build in
existence at a time. The shell is served `no-cache` and content-hashed assets `immutable`
(`backend/internal/web/embed.go:69-97`), and `push-sw.js` is push-only — it deletes every cache on
activate (`frontend/public/push-sw.js:39-46`). A reload therefore always lands on the current
build; only a tab left open across a deploy can be stale.

## 3. The Amika org is a real thing, and it is not this

The obvious reading of "he shares Kiln's org" conflates two different orgs. `35b2d3e` (2026-08-10)
amended 11 §3 with the real one: the Amika **account** is per-project-owner, but Amika enforces its
sandbox concurrency cap per *organization*, so two Kiln users whose stored keys belong to one Amika
org share one pool and can starve each other. That is a genuine shared-fate mechanism — it is just
not one that can hide a Poke button, for a sequencing reason:

`RunPull` moves a ticket Ready→Working and binds a board worker slot **inside its transaction**,
then emits `agent.send` (`backend/internal/board/service.go:836-856`, `emitPullEffects`). The Amika
sandbox is created afterwards and asynchronously, by the agent module's turn machine
(`stepEnsureReady` → `ensureWorker`, `backend/internal/agent/service.go:710-726`). Board capacity is
`worker_count` slots, not Amika sandboxes — "the real pull never fails this way — it simply waits
for capacity" (`backend/internal/board/errors.go:22`).

So a starved Amika pool cannot keep a ticket out of Working. The ticket is *already* Working when
the create fails; the failure counts against the retry budget and fails the turn
(`recordFailure`, `service.go:830-849`), leaving the ticket Working with an errored agent until the
brain moves it Blocked. **Both of those states show the 👉.** If anything, pool starvation produces
*more* poke-eligible tickets, not fewer.

## 4. What can actually differ

With the client code byte-identical for every user, only these remain:

1. **Ticket state.** No 👉 on shaping/ready/done — only working/blocked. The most likely
   explanation, and it is data, not configuration.
2. **A live voice session.** Poke, Delete and Accept withdraw as a group while the mic is up, so
   the trailing slot can become Send and × without anything sliding (`TicketDetail.tsx:1034`,
   `inVoiceMode`).
3. **Emoji rendering.** See §5.
4. **Looking at the feed, not the sheet.** The feed's 👉 poke card is a different surface: the
   steward posts it only after a Working ticket's agent has sat `idle`/`stopped` for
   `KILN_POKE_STALL` (default 5m, `backend/internal/steward/config.go:11-12`), and exactly once per
   Working episode (`steward/service.go`, `evaluate`/`poke`).

**The one question that discriminates:** *on a finished ticket, do you see the ✅?* Sees it → emoji
rendering is fine, so it is (1) and the next question is which column the ticket sits in. Does not
see it → §5, and it is not a Poke bug at all.

## 5. Open: no emoji in the client has a font fallback

`--font-sans` is `'Hanken Grotesk Variable', Seravek, 'Segoe UI', system-ui, sans-serif`
(`frontend/src/styles/tokens.css:100`). No emoji family is appended, and no rule anywhere in the
client adds one. Every emoji rides that stack: the detail button's 👉 via `font-family: inherit`
(`frontend/src/components/TicketDetail.css:914-918`), the feed poke card's 👉
(`FeedCardItem.tsx:354`, `PrimaryScreen.css:1131`), the done card's ✅, and the toast verb glyphs
(`feed-format.ts:230`).

Browsers do fall back to a system emoji font for characters the stack cannot render, so this is
only a problem where the OS ships none — but there it is systemic, not Poke-specific, and Poke is
its worst case: the button's *entire* visible content is the glyph, so it degrades to a blank
54px disc with no label to explain it. The `aria-label` keeps it reachable for assistive tech and
for the layout tests, which is why nothing in the gate would catch this.

**Suggested follow-up ticket:** append an emoji family to `--font-sans` — the usual
`'Apple Color Emoji', 'Segoe UI Emoji', 'Noto Color Emoji'` tail. One token, and it covers all four
call sites above.

## 6. Limits

Not verified against production or against the reporting user's account: no local stack was running
during this investigation and there is no read path to the prod database from here. §1–§3 are read
off the code and the deploy topology and are firm; §4 is a candidate set, deliberately narrowed to
what the code leaves possible rather than picked between.
