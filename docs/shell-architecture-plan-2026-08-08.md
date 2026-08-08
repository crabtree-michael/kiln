# Shell architecture — how mobile and desktop stop being copies of each other

**Date:** 2026-08-08
**Status:** plan, for approval. No implementation code has been written.
**Answers:** `docs/dev-velocity-review-2026-08-08.md` D1 (and absorbs D9), and spec `13` §13 Q4.

---

## 0. The one-paragraph version

Spec 13's current answer to "how do mobile and desktop share one client" is *one responsive tree,
two shells over shared **stores***. That is what shipped, and it is why `PrimaryScreen.tsx` is
already a single wiring seam. The problem is that it shares only the stores: **everything between
the stores and the DOM is hand-copied**, and there are now three shells copying it. The proposed
answer is the same sentence with more layers in it: **shells share stores, intents, the feed
reading model, and overlay behaviour, and differ only in DOM shape and CSS.** A shell becomes a
`return (…)` and nothing else — enforced by a lint rule and a conformance test suite, not by a
convention comment that the fourth shell's author will not read.

---

## 1. What is actually duplicated today

The review reports 176 verbatim lines between the two feed shells. I re-measured: **187 distinct
trimmed lines in common** between `PrimaryScreenView.tsx` and `DesktopScreenView.tsx` (245 counting
repeats). Same finding, marginally larger. But the line count understates it in two ways: it
excludes `KanbanScreenView.tsx`, which is a third copy of part of it, and it excludes the
**container** axis entirely, which the review does not mention and which is where the next copy
will appear.

### 1a. Views — `PrimaryScreenView` (616) / `DesktopScreenView` (562) / `KanbanScreenView` (346)

**Module-level pure functions, byte-identical:**

| Function | Mobile | Desktop | Kanban |
|---|---|---|---|
| `updateId(card)` | `PrimaryScreenView.tsx:151` | `desktop/DesktopScreenView.tsx:114` | — |
| `dividerIndex(cards, lastSeenId)` (17 lines) | `:175` | `:121` | — |
| `isSeen(card, lastSeenId)` | `:198` | `:140` | — |
| `findTicket(board, id)` | `:210` | `:151` | `desktop/KanbanScreenView.tsx:82` |
| `EMPTY_SUMMARY` | `:31` | `:58` | — |
| `dismissableId(card)` | `:162` | — | — |

`dismissableId` is mobile-only but is the *same taxonomy* as `stores/feed-store.tsx:54-57`'s
`hidden()` predicate, spelled differently — that is review D9 showing up inside D1.

**Derivation prologue, identical in both feed shells:**
`summary = feed?.summary ?? EMPTY_SUMMARY`, `cards = feed?.cards ?? []`,
`lastWord = lastWordDetail(summary, now)`, `divider = dividerIndex(cards, lastSeenId)`.

**The ticket-overlay state cluster — identical in all three shells:**

```
const [openTicketId, setOpenTicketId] = useState<string | null>(null);
const closeTicket = useCallback(() => { setOpenTicketId(null); }, []);
useDeepLinkTicket(setOpenTicketId);
const [ticketVoiceActive, setTicketVoiceActive] = useState(false);
const openTicket = findTicket(board, openTicketId);
const openAgentStatus = openTicket === null ? undefined
  : board?.agents.find((agent) => agent.ticket_id === openTicket.id)?.status;
```

**The `<TicketDetail>` block — three copies, identical except one prop.** 15 props each, including
three hand-written close-after-action wrappers (`onAccept`, `onDelete`, `onPoke`). `PrimaryScreenView.tsx:529-613`,
`desktop/DesktopScreenView.tsx:504-559`, `desktop/KanbanScreenView.tsx:298-343`. The only real
difference is `placement="right"` on the two desktop ones.

**Small JSX copies:** the `Show earlier` button and its `hasEarlier && onShowEarlier !== undefined`
guard (`:493` / `:472`); the `feed-divider` element (`:454` / `:428`).

**Props interfaces:** 23 fields of `DesktopScreenViewProps` are re-declarations of
`PrimaryScreenViewProps` fields — with the doc comments **re-written by hand and already shorter**,
which is a drift surface in its own right (the desktop copy has lost the reasoning the mobile one
carries).

### 1b. Containers — `PrimaryScreen.tsx` (325) / `KanbanScreen.tsx` (177)

Not in the review's D1, same disease. The seven ticket action callbacks are duplicated:

- **Byte-identical bodies** (comments differ): `onPoke`, `onSetKeepSandbox`, `onKillSandbox`,
  `onReassignSandbox`, `onEditText` — all five including the `.catch(() => { refreshBoard(); })`
  failure-recovery shape.
- **Differ by exactly one line**: `onAccept` and `onDelete` (mobile adds the feed store's
  optimistic `acceptProposal` / `deleteTicketCard` hide; Kanban has no feed to hide).
- **The `railProjects` map** is identical: `PrimaryScreen.tsx:101-112`, `KanbanScreen.tsx:62-70`.
- Provider stack + `usePresence()` + `useNotificationMode()` + `useWebPush()` + `useProjectsStatus()`
  bridging is repeated per container.

### 1c. Tests

`PrimaryScreenView.test.tsx` (1459) and `DesktopScreenView.test.tsx` (1138) assert the **same feed
semantics** against different DOM: the last-seen divider, "Show earlier" in every state, seen
de-emphasis, the Accept↔mic-Send swap during a voice session, the sandbox flows. Fixing a shared
rule means editing two suites, and there is nothing that fails if you only edit one.

---

## 2. The proposed architecture

Four layers. Only the top one is per-shell.

```
L1  intents        screens/ticket-intents.ts        useTicketActions()      what we may DO
L2  reading model  components/feed-model.ts         readFeed() → FeedRow[]  what there IS to show
L3  behaviour      components/use-ticket-overlay.ts useTicketOverlay()      what we REMEMBER
                   components/TicketDetailHost.tsx  <TicketDetailHost>
L4  shells         PrimaryScreenView / DesktopScreenView / KanbanScreenView  what it LOOKS like
```

### L1 — Intents: `useTicketActions()`

One hook returning the seven callbacks, owning "what the client is allowed to do to a ticket, and
how a failed write recovers." No DOM, no feed knowledge. All containers consume it.

The optimistic feed hides are **injected, not read**:

```ts
useTicketActions({ onAcceptOptimistic?: (id) => void, onDeleteOptimistic?: (id) => void })
```

`PrimaryScreen` passes the feed store's `acceptProposal` / `deleteTicketCard`; `KanbanScreen`
passes nothing. This is deliberate: `/kanban` does not mount `FeedProvider` (by design — a board
view costs no `GET /api/feed`), so the hook must not have a feed dependency. The alternative
— a `useOptionalFeedStore()` that returns `null` outside a provider — hides the coupling and makes
"does this route have a feed?" invisible at the call site. **Decision worth confirming: injection.**

### L2 — Reading model: `readFeed()` → `FeedRow[]`

A plain `.ts` module (no components, so `react-refresh/only-export-components` stays quiet — the
convention `feed-format.ts` already documents). It absorbs every function from §1a plus the
card-kind predicates, and — the important part — it returns the feed **already decided**:

```ts
interface FeedRow { card: FeedCard; seen: boolean; dismissId: number | null; dividerBefore: boolean }
interface FeedReading { summary: FeedSummary; rows: FeedRow[]; isEmpty: boolean;
                        lastWord: string | null; hasClearable: boolean }
function readFeed(feed: FeedSnapshot | null, lastSeenId: number | null, now: number): FeedReading
```

Each shell's card loop becomes `reading.rows.map(row => …)`, choosing only its own element type
(`<div data-role="backlog-slot">` vs `<li><div data-role="desktop-feed-row">`). **Membership,
order, the divider position, seen-ness and dismissability are decided in exactly one function.**
That is what makes the "same conceptual bug fixed five times, alternating shells" class of failure
impossible rather than merely unlikely.

### L3 — Behaviour: `useTicketOverlay()` + `<TicketDetailHost>`

The hook returns the whole §1a state cluster as one object
(`{ openTicketId, openTicket, openAgentStatus, openTicket: …, setOpenTicketId, closeTicket, voiceActive, setVoiceActive }`).
The component renders the 15-prop `TicketDetail` block once, taking `placement` as a prop and
keeping **per-action optionality** (see the risk in §5). All three shells use both.

### L4 — Shells

Markup, CSS imports, platform-only affordances. Nothing else.

### The rule that makes it durable

> **A shell file contains no `function` declaration and no `useState` above its component body.**
> If it computes what to show → L2. If it remembers something → L3. If it writes to the server → L1.

Enforced two ways, because a documented rule is what we already have:

1. **An eslint `no-restricted-syntax` rule** scoped to the three shell files, banning
   program-level `FunctionDeclaration` and `useState` calls. Mechanical, cheap, and it fires on
   the fourth shell before it is reviewed.
2. **A shared conformance suite** — `describe.each` over a table of shell adapters
   (`{ render, rowSelector, dividerSelector, showEarlierSelector }`) asserting the *shared*
   semantics once against both DOMs. A fix to the shared model is then asserted in both shells by
   one test, which is precisely the guard that was missing during the five-commit saga.

### Alternatives considered and rejected

- **One shell restyled by media queries.** Already rejected in 13 D8 and in
  `desktop/use-desktop-layout.ts`'s own comment: the two layouts do not share a DOM shape. Not
  revisited here.
- **One `<FeedScreen variant="mobile"|"desktop">` with internal branches.** The tempting one, and
  worse than today: it moves the duplication *inside* a file as conditionals, where every branch is
  a place the two shells can diverge without a second file to notice it.
- **Compound components / slot API (`<FeedScreen><FeedScreen.Header>…`).** Over-built for two
  shells; the slot contract becomes the new coupling surface and the new drift surface.
- **Extracting a shared stylesheet.** Already effectively done — `DesktopScreen.css` layers over
  `PrimaryScreen.css`'s unscoped rules, and that mechanism works. Splitting `PrimaryScreen.css` is
  review D3, deliberately **out of scope**.

---

## 3. Shared vs presentation-specific

**Shared (moves into L1–L3):** feed reading model and card-kind taxonomy; the seven ticket intents
and their failure-recovery shape; overlay open/close state; deep-link → open-ticket; the
voice-active boolean seam; the whole `TicketDetail` wiring; `EMPTY_SUMMARY`; *whether* "Show
earlier" renders; *where in the card order* the divider falls.

**Stays per-shell (L4), and should:** all DOM and CSS; mobile's header / `HeaderStatusMenu` /
`ProjectSwitcher` brand slot vs desktop's `DesktopRail`; `Dock` + `useKeyboardViewport`
(mobile only); `SwipeToDismiss` + clear-all trash (mobile only, per 13 §6 — Q3 is still open);
pull-to-refresh (mobile only, a touch gesture); arrow-key roving focus (desktop only);
`WorkingNow` / `Backlog` columns (desktop only); the loading line (desktop only); the resting-state
copy, which genuinely differs in wording; `moreLabel="more"`; `placement="right"`;
`useDesktopShellFlag()`.

**Resulting shape, roughly:** `PrimaryScreenView` 616 → ~330, `DesktopScreenView` 562 → ~300,
`KanbanScreenView` 346 → ~250, plus ~250 lines of new shared modules. Net LOC is roughly flat.
**The win is not line count** — it is that each decision exists once and the type checker and the
conformance suite both know it.

---

## 4. Behaviour differences the shared logic must preserve

Fourteen, found by reading both shells against each other. Each is a way a naïve extraction breaks
visible behaviour.

1. **`loading` is desktop-only.** `PrimaryScreen.tsx:83` computes `boardLoading || feedLoading` and
   passes it *only* to the desktop view. Mobile shows no loading indication at all. Shared code
   exposing `loading` must not start rendering a line on the phone.
2. **The resting state differs in words *and* in gating.** Mobile always renders `feed-empty`
   (`streamDetail` + optional last-word subtext). Desktop renders "All quiet." and **withholds it
   while `loading`**, because it is a claim that we asked and there was nothing
   (`desktop/DesktopScreenView.tsx:411`, with a test at `DesktopScreenView.test.tsx:338`). `isEmpty`
   must stay a fact; the gating stays in the desktop shell.
3. **The divider's wrapper element differs** (mobile: inside `backlog-slot`; desktop: inside `<li>`
   before `desktop-feed-row`) while its index is shared. DOM snapshots key on this.
4. **"Show earlier" must stay last *inside each shell's own scroll wrapper*** — mobile's
   `[data-role='backlog']`, desktop's `[data-role='desktop-feed-scroll']` — because its
   `position: sticky` hangs off that containing block, and `margin-top: auto` off the same box.
   Both files carry a comment begging you not to move it. A shared component must be **composed
   into** each wrapper, never hoisted to a common parent. **This is the single highest regression
   risk in the whole plan** and it is the exact bug that was fixed five times.
5. **Dismissal is mobile-only and that is a product decision**, not an omission (13 §6, Q3 open).
   Moving `dismissableId` into L2 must not imply desktop grows a swipe or a hover-close.
6. **Clear-all's `window.confirm`** is mobile-only.
7. **`now` must stay injectable** (both shells default it to `Date.now()`); the deterministic
   snapshot tests depend on it.
8. **`/kanban` has no feed at all** — no `FeedProvider`, no feed fetch. L1 must not require one
   (§2, L1).
9. **`board` can be `null`** in all three shells; the overlay hook must tolerate it.
10. **`openAgentStatus` and `canReassign`** read the same board fields in all three — safe to share
    verbatim.
11. **The voice-active boolean is per-shell state by design** (documented in the web-client skill);
    a hook naturally keeps it per-mount, which is correct.
12. **`useDeepLinkTicket` is called once per shell**, and the shell switch is exclusive, so there is
    never a double subscription. Keep it exclusive.
13. **jsdom has no `matchMedia`, so `useIsDesktop()` is `false`** — every existing shell test
    renders the *view* directly. The conformance suite must do the same, not drive `PrimaryScreen`.
14. **`--dock-overlay-height`** is published by the mobile Dock and absent on desktop (falls back to
    0), and the shared `ActivityRow` already handles both. Do not "unify" the publisher.

---

## 5. Risks and complexity

- **DOM snapshots are the tripwire, and should not move.** `PrimaryScreenView.test.tsx` holds
  structure snapshots for 4a/4b/4c/4d and the proposal card. The extraction is prop-shape-only, so
  **any snapshot that changes means something visible moved.** Rule for the implementation tickets:
  no `-u`. If a snapshot needs updating, stop and explain why.
- **jsdom cannot see the layout risk in §4.4.** The CSS-as-string tests
  (`PrimaryScreen.show-earlier.test.ts`, `desktop/DesktopScreen.layout.test.ts`) catch the
  declarations; only `tests/desktop-shell-smoke.mjs` and `tests/show-earlier-skirt-repro.mjs`
  measure real geometry, and both are **hand-run, outside the gate**. Mitigation: capture their
  measurements before the work and diff after. Better: land review D2 first (below).
- **Per-action optionality is a test seam, not an accident.** Every `on*?` prop is optional so
  presentational tests can omit it and assert the affordance is *absent*. A `TicketDetailHost`
  taking one `actions` object must preserve that, or roughly ten tests quietly change meaning.
- **Callback identity.** `useTicketActions()` must return referentially stable callbacks or
  `FeedCardItem` / `TicketDetail` re-render behaviour shifts. Worth one explicit test.
- **Merge collisions.** These are the repo's hottest files (14 cross-shell commits in the desktop
  shell's first four days; ~30% of history is merges). Land this as a few small sequenced commits,
  not one long-lived branch.
- **The lint rule will occasionally be wrong.** `KanbanScreenView`'s `cardLabel` is a legitimate
  shell-local formatter. Either move it to L2 or accept a short documented allowlist — don't let the
  rule's purity push presentation logic into shared modules.
- **One genuinely arguable call, flagged rather than decided:** whether `KanbanScreenView` is in
  scope. It shares the overlay and the intents but no feed. Including it triples the payoff of L1
  and L3 for little extra work; excluding it leaves a third live copy of `findTicket` and of the
  `TicketDetail` block. **Recommendation: include it, in T3/T4 rather than T1/T2.**

---

## 6. Shape of the implementation ticket(s)

**Split — five tickets (plus one optional prerequisite).** Each is independently green, independently
revertable, and small enough to review against the snapshot rule above.

| # | Ticket | Touches | Size | Depends on |
|---|---|---|---|---|
| **T0** | *(optional, recommended)* Put the two geometry smokes in the gate; retire the hand-run `.mjs` scripts into it | `tests/`, Makefile | S | — |
| **T1** | L2 part 1: extract `feed-model.ts` (the six pure functions + card-kind predicates), both feed shells import it. Absorbs review **D9** | 2 views, feed-store, new `.ts` + test | S | T0 |
| **T2** | L2 part 2: `readFeed() → FeedRow[]`; both shells' card loops consume rows. **Carries the §4.4 layout risk** | 2 views | M | T1 |
| **T3** | L3: `useTicketOverlay()` + `<TicketDetailHost>`; all **three** shells | 3 views, 2 new files | M | T2 |
| **T4** | L1: `useTicketActions()`; both containers | `PrimaryScreen.tsx`, `KanbanScreen.tsx`, new `.ts` | S | T0 — **parallelisable with T1–T3** (different files) |
| **T5** | Durability: shared conformance suite, the eslint shell rule, update the web-client skill's "two views over one wiring seam" section and 13 §13 Q4's answer in the decision log | tests, eslint config, docs | M | T3, T4 |

**Sequence:** T0 → (T1 → T2 → T3) with T4 running in parallel → T5.
**Rough size:** about a week of focused work for one agent; two to three days with T4 parallelised.
Consistent with the review's own "Medium complexity, weeks 2–3" placement, and with its note that
D1 is the thing to do first if only one thing gets done.

**On T0.** The review lists it as D2 and recommends doing D1 *after* it, "so the layout suite guards
the move." I agree, and T2 is the specific reason. If T0 is declined, T2 should be sequenced last
and its before/after geometry measured by hand — that is a real reduction in safety, not a
formality.

**What is deliberately not in any ticket:** splitting `PrimaryScreen.css` (review D3), any visual or
layout change to either shell, opening 13 §13 Q3 (desktop dismissal), and every other duplication
finding from the audit unrelated to the feed screen.
