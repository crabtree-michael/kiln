# Shell architecture — how mobile and desktop stop being copies of each other

**Date:** 2026-08-08 (revised same day)
**Status:** **ready for review — no open decisions.** Every question this plan raised has been
answered; the ticket chain in §6 can start on approval. No implementation code has been written.
**Answers:** `docs/dev-velocity-review-2026-08-08.md` D1 (and absorbs D9), and spec `13` §13 Q4.

**Decisions taken (2026-08-08), both settled:**

- **`KanbanScreenView` is IN SCOPE**, per §5's recommendation — it joins at **T3/T4**, not T1/T2.
  That settles the "one genuinely arguable call" §5 flagged.
- **The feed's optimistic card hides are INJECTED**, not looked up ambiently — the shared ticket
  intents take the hide as an input rather than reaching for a feed store. §7 is the record of
  that decision, including the case for the alternative that was not taken.

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

> **The trap inside that table, and the single most important thing for whoever implements T1.**
> `updateId` is defined **three times meaning two different things**. In both feed shells it is
> `update|preview` — the last-seen divider boundary, which is a claim about what Kiln has *said*,
> so the mechanical `poke`/`done` notices are deliberately excluded. In `feed-store.tsx:63` a
> function of the *same name* covers all four notification-backed kinds
> (`update|preview|poke|done`) — the history cursor and the swipe's retract id.
>
> An extraction that unifies them by name — which is exactly what "these look identical, merge
> them" produces — silently slides the divider onto the poke and done cards. Nothing in the gate
> would catch it: it type-checks, and no test asserts the two sets differ, because until they
> share a module there is no place to write that assertion. **T1 must give them two names and
> pin the disagreement in a test.** (Confirmed by building T1 on a scratch branch, since
> discarded; this finding is the part worth keeping.)

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
view costs no `GET /api/feed`), so the hook must not have a feed dependency.

**Decided (2026-08-08): injection.** The alternative — a `useOptionalFeedStore()` returning `null`
outside a provider — hides the coupling and makes "does this route have a feed?" invisible at the
call site, which is the failure mode this whole plan exists to reverse. §7 is the full record of
the choice, including the honest case for the option not taken and the conditions that would
justify revisiting it.

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
- ~~**jsdom cannot see the layout risk in §4.4.**~~ **RESOLVED by T0, which has landed** (see §6).
  When this was written the only things measuring real geometry were two hand-run `.mjs` scripts
  outside the gate, and the mitigation on offer was "capture their measurements before the work
  and diff after". That is no longer the situation: `tests/layout/` now drives a real browser in
  `make check` and asserts §4.4's invariant directly — the control's standoff from the dock is
  identical in every toast-band state, and the band paints *over* the control rather than moving
  it. The six `.mjs` scripts are deleted. **T2 is now guarded rather than merely careful**, which
  is the single biggest change to this plan's risk profile since it was written.
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
- ~~**One genuinely arguable call, flagged rather than decided:** whether `KanbanScreenView` is
  in scope.~~ **DECIDED 2026-08-08: in scope, joining at T3/T4.** It shares the overlay and the
  intents but no feed. Including it triples the payoff of L1 and L3 for little extra work;
  excluding it would have left a third live copy of `findTicket` and of the `TicketDetail` block.
  Two consequences for the tickets below: T3 covers **three** shells rather than two, and T4
  covers **both** containers. T1/T2 stay feed-shells-only, since the kanban shell has no feed —
  its only T1-era duplicate is `findTicket`, which T3 removes when it adopts the overlay hook.

---

## 6. Shape of the implementation ticket(s)

**Split — five tickets (plus one optional prerequisite).** Each is independently green, independently
revertable, and small enough to review against the snapshot rule above.

| # | Ticket | Touches | Size | Depends on |
|---|---|---|---|---|
| ~~**T0**~~ | ✅ **LANDED 2026-08-08** — the layout gate. Delivered wider than specified; see below | `tests/layout/`, `tokens.css`, Makefile | — | done |
| **T1** | L2 part 1: extract `feed-model.ts` (the six pure functions + card-kind predicates), both feed shells import it. Absorbs review **D9** | 2 views, feed-store, new `.ts` + test | S | ✅ satisfied |
| **T2** | L2 part 2: `readFeed() → FeedRow[]`; both shells' card loops consume rows. **Carries the §4.4 layout risk** | 2 views | M | T1 |
| **T3** | L3: `useTicketOverlay()` + `<TicketDetailHost>`; all **three** shells | 3 views, 2 new files | M | T2 |
| **T4** | L1: `useTicketActions()`; both containers | `PrimaryScreen.tsx`, `KanbanScreen.tsx`, new `.ts` | S | ✅ satisfied — **parallelisable with T1–T3** (different files) |
| **T5** | Durability: shared conformance suite, the eslint shell rule, update the web-client skill's "two views over one wiring seam" section and 13 §13 Q4's answer in the decision log | tests, eslint config, docs | M | T3, T4 |

**Sequence:** ~~T0 →~~ (T1 → T2 → T3) with T4 running in parallel → T5. T0 is done, so the chain
starts at T1 and T4 together.
**Rough size:** about a week of focused work for one agent; two to three days with T4
parallelised — now less, since T0 is no longer part of it. Consistent with the review's own
"Medium complexity, weeks 2–3" placement, and with its note that D1 is the thing to do first if
only one thing gets done.

**Status of the table (2026-08-08).** Both decisions are locked in above: T3's "three views" and
T4's "both containers" are settled rather than proposed, and T4's shape is fixed by §7's
injection decision — it takes `onAcceptOptimistic` / `onDeleteOptimistic` as inputs. T0 has
landed, so nothing is waiting on a prerequisite either. **No ticket is blocked.** The chain runs
on approval.

**On T0 — landed, and wider than this plan asked for.** The review listed it as D2 and recommended
doing D1 *after* it, "so the layout suite guards the move." That is now history rather than a
recommendation: the gate exists, in
`f6f900f` (merge) / `4985f18` (the suite) / `cb4711b` + `922537d` (the notes).

What shipped goes beyond the row above, which asked only for the two geometry smokes to be
gated:

- **`tests/layout/` — 32 specs across five files, real browser, every `/api` call stubbed.** No
  stack and no keys, so it runs inside the hard gate (`make check` → `test` → `test-layout`,
  ~1 min) rather than beside the e2e suite.
- **The eleven `?raw` string-matching test files (~2000 lines) are gone**, replaced by measured
  geometry. This plan never asked for that; it is the review's D2 taken to its conclusion. What
  is genuinely about a file's *text* survives in `stylesheet-discipline.test.ts` — you cannot
  observe the absence of a colour in a rendering.
- **The six hand-run `.mjs` scripts are deleted.** §5's risk bullet about them is struck through.
- **The stacking order is now one `--layer-*` scale in `tokens.css`**, asserted as real paint
  order.

**Three consequences for this plan, all favourable:**

1. **§4.4 — the highest regression risk named here — is now covered by an assertion**, not by
   care. The gate checks both halves of the recurring bug: the control's standoff from the dock
   is identical in every band state, and the band paints over the control rather than moving it.
2. **T2 loses its "sequence it last and measure by hand" caveat entirely.** It is now an ordinary
   ticket with a gate behind it.
3. **T5 shrinks.** It no longer needs to reason about which `?raw` assertions the extraction made
   redundant; that cleanup already happened.

One caution that survives, and is now the *only* thing jsdom-blindness costs this plan: the
layout gate measures the shells as they render, so it protects §4.4 against a **structural**
mistake (hoisting the control out of its scroll wrapper). It says nothing about the DOM-snapshot
tripwire in §5 — that remains the check that the extraction moved no markup, and it is still the
one to watch during T2 and T5.

**What is deliberately not in any ticket:** splitting `PrimaryScreen.css` (review D3), any visual or
layout change to either shell, opening 13 §13 Q3 (desktop dismissal), and every other duplication
finding from the audit unrelated to the feed screen.

---

## 7. Decision record — how the "card vanishes instantly" reaches shared code

**DECIDED 2026-08-08: Option A, injection.** Kept in full below as the record of *why*, because
the reasoning matters more than the outcome: this is a small, reversible engineering choice that
nonetheless sets a habit the rest of the plan leans on, and a future reader deserves the argument
rather than the verdict. §7.5 preserves the honest case for the option not taken, and names the
condition under which revisiting it would be right.

### 7.1 What the thing actually is

When you tap **Accept** on a proposal card in the feed, the card disappears **immediately** —
before the server has confirmed anything. That instant disappearance is what makes the tap feel
responsive instead of laggy. The server confirms a moment later over the live connection, and if
the accept somehow fails, the card comes back on its own. Internally that instant-vanish is
called the *optimistic hide*.

The important fact: **the board view at `/kanban` has no feed and therefore no cards to vanish.**
It shows columns of tickets, not a feed. Accepting a ticket there just moves it between columns,
which happens on its own when the server confirms. So the optimistic hide is a thing that exists
on *some* screens and not others.

The plan puts "accept a ticket" (and delete, poke, and four more) into **one shared piece of code
used by every screen** — that is the whole point of layer L1. The question is: **how does that
shared code know whether this screen has a feed with a card to hide?**

### 7.2 The two options

**Option A — Injection ("each screen says what it needs"). ← CHOSEN**
The shared code doesn't look for a feed. It accepts a hide-the-card instruction as an *input*.
The feed screen hands it one. The board screen hands it nothing, and the shared code simply
skips that step.

**Option B — Ambient lookup ("the shared code looks around for a feed").**
The shared code asks its surroundings "is there a feed on this screen?" It gets back either a
feed or a "no feed here", and hides the card only in the first case. Screens pass nothing.

Both produce **identical behaviour today**. Nothing a user sees differs. The difference is
entirely in what a future developer — or a future agent — can tell at a glance.

### 7.3 Side by side

| | **A — Injection** *(CHOSEN)* | **B — Ambient lookup** *(not taken)* |
|---|---|---|
| What each screen writes | One extra line on the feed screen; the board screen deliberately writes nothing | Nothing on either screen |
| Reading the board screen, can you tell it has no feed? | **Yes** — the omission is right there, and can carry a comment saying why | **No** — you must open the shared file and reason about it |
| A new screen that forgets to set up its feed | Author is forced to decide, because the input is staring at them | Silently gets no card-hide. No error, no failing test — just a tap that feels sluggish, found by a user |
| Where the "does this route have a feed?" fact lives | At each screen, visibly | Implied, in shared code, invisibly |
| Risk of quietly re-coupling the board view to the feed | Low — a feed dependency would have to be typed out | Higher — the shared code now knows how to find a feed, so reaching for one becomes the easy path |
| Lines of code | ~2 more, total | ~2 fewer |
| Testing the shared code | Straightforward — hand it a fake, or nothing | Needs a fake "surroundings" to test the no-feed case |
| Cost to switch to the other later | **Low.** One file, two call sites, an afternoon. Fully reversible either direction. | Same |

### 7.4 Why A was chosen

Three reasons, in order of weight:

1. **The whole premise of this plan is that decisions go wrong when they become invisible.** §1
   documents three shells that drifted apart precisely because each copy's reasoning lived
   nowhere the next author would look. Option B takes one decision that is currently visible —
   "this route has no feed, on purpose" — and hides it again, inside the very code being written
   to stop that happening. That is a small step in the direction the plan exists to reverse.
2. **`/kanban` not loading the feed is a deliberate performance choice, and a fragile one.** It
   saves a network request on every visit to the board view. It is the kind of thing that gets
   undone by accident. Option A makes undoing it require typing something; Option B makes the
   machinery for undoing it already present.
3. **Option B's failure mode is silent, and silent is the expensive kind.** A screen that
   forgets its feed under Option B produces no error and no failing test — just a tap that feels
   slightly wrong, which someone eventually reports as a vague bug. Under Option A the same
   mistake is visible while the code is being written.

### 7.5 The honest case for B

Not a strawman — it wins under one condition. **If the number of screens needing the optimistic
hide grows to five or six, the repeated wiring becomes noise**, and noise is its own kind of
invisibility: people stop reading lines they see everywhere. At that point B's brevity is worth
more than A's explicitness.

Today there are **two** screens, and the plan adds none. So the condition that would favour B
does not hold yet — and because switching costs an afternoon, adopting A now costs nothing if
that changes later.

### 7.6 What this binds, and when to reopen it

**Binding on T4:** `useTicketActions()` takes `onAcceptOptimistic` and `onDeleteOptimistic` as
optional inputs. `PrimaryScreen` passes the feed store's `acceptProposal` / `deleteTicketCard`;
`KanbanScreen` passes neither, with a comment saying why. There is to be **no
`useOptionalFeedStore()`**, and no other route by which shared ticket code reaches for a feed
store — that is the whole content of this decision, and a later "small convenience" that
reintroduces one is this decision being reversed by accident rather than on purpose.

**Not binding on anything else.** No other ticket changes shape either way.

**Reopen it when — and only when — §7.5's condition actually arrives:** five or six screens
each wiring the same two hides by hand. At that point the repetition has become noise and B is
the better answer. Reopening costs an afternoon (§7.3, last row), so there is no need to
pre-empt it; the trigger is a count, not a feeling.

**One rule that survives either option**, worth stating because it is the thing actually at
stake: *whether a route has a feed must be visible at that route.* Injection achieves it by
making the screen say so. If B is ever adopted, it has to achieve the same thing some other
way — a comment at the screen, a named type, something — rather than simply dropping the fact.
