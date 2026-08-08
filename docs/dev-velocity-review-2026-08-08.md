# Kiln — Architecture Review: Development-Velocity Hotspots

**Date:** 2026-08-08
**Scope:** Where the architecture is weak *in ways that slow down development*.
**Explicitly out of scope:** runtime performance, model cost, latency (covered by the separate
backend investigation ticket). No code changes were made; this is a plan only.

**Method.** Structural read of `/backend`, `/frontend`, `/schema`, `/tests`, plus quantitative
analysis of all 797 commits: per-file churn, per-file bug-fix counts, file-count amplification per
commit, merge overhead, and a before/after measurement of every unit the 2026-07-08 audit flagged
(sizes taken from `git show <rev>:<path>` at the commit boundary, so they are comparable).
Gate timings were measured by running it.

---

## 1. Summary

Kiln's *module* architecture is not the problem. The backend seams are real, the ports are honest,
the fakes are hand-written, and the gate is genuinely fast (backend build 2.5 s, backend tests
10.7 s, frontend 1054 tests in 40 s — the whole wall is under a minute). Nobody is waiting on CI.

The drag is concentrated in three places, and they are different in kind:

1. **The frontend has two hand-copied UI shells.** The desktop shell (landed 2026-08-04, four days
   ago) shares **176 distinct lines verbatim** with the mobile shell, including whole functions.
   Every feed behaviour change now costs two implementations, and every bug gets fixed twice. This
   is already visible in the history and it is the single largest tax.
2. **Layout invariants live in prose and in string-matched CSS, not in code.** The web-client skill
   doc is 1209 lines — 45% of all skill docs combined — and is the *second most bug-fix-touched
   file in the repo*. 11 test files (1945 LOC) "test" CSS by `indexOf`-ing selectors out of the
   stylesheet source. The result is a class of bug that reliably reappears.
3. **Every feature has to be threaded through a growing composition root and an
   optional-dependency API server.** A client-facing feature touches ~24 files on average; the last
   one touched 35. `api.Server` has 7 constructor args, 14 `Enable*` setters and 63 nil-guards.

There is also a **meta-finding worth stating plainly**: a thorough audit was written on 2026-07-08
(`docs/architecture-audit-2026-07-08.md`) with four "do first — cheap, high leverage" items. One
month later, **all four are still open**, and a detailed split plan was written for
`runtime/service.go` (`docs/god-units-plans/runtime-service.md`) and never executed. Meanwhile
every unit that audit flagged has grown. Whatever this review recommends will meet the same fate
unless the recommendations are small enough to land inside a normal ticket. That constraint shaped
the prioritisation below.

### Growth of every previously-flagged unit (2026-07-08 → 2026-08-08)

| File | Then | Now | Δ |
|---|---:|---:|---:|
| `backend/internal/api/routes.go` | 1050 | 1779 | **+69%** |
| `backend/cmd/kiln/wiring.go` | 787 | 1118 | **+42%** |
| `frontend/src/stores/feed-store.tsx` | 537 | 734 | +37% |
| `frontend/src/components/PrimaryScreenView.tsx` | 453 | 616 | +36% |
| `backend/cmd/kiln/adapters.go` | 880 | 1137 | +29% |
| `backend/internal/agent/service.go` | 878 | 1136 | +29% |
| `backend/internal/brain/tools.go` | 1045 | 1221 | +17% |
| `backend/internal/runtime/service.go` | 840 | 941 | +12% |
| `frontend/src/dashboard/ConfigFields.tsx` | 587 | 524 | −11% |

Eight of nine grew. `ConfigFields.tsx` is the only one that shrank — and it is the only one that
got a targeted cleanup.

---

## 2. What is working — do not "fix" these

- **Backend module boundaries are real.** Cross-module talk goes through ports wired at one
  composition root. This is the thing that makes the backend safe for parallel agents; nothing in
  this report should erode it.
- **The gate is fast and green.** 1054 frontend tests + full backend suite in well under a minute.
  Feedback-loop latency is *not* a velocity problem here, and I found no reason to touch it.
- **Test-to-code ratio is earned, not mock bloat.** Backend 32,973 test LOC to 25,385 prod LOC;
  frontend 24,519 to 21,407. Hand-written fakes, no mock framework.
- **Escape-hatch hygiene** remains genuinely clean.
- **`tokens.css`** (92 design tokens, one file) is the right shape and is being used. The problem
  described in §4.3 is *layout*, not colour — the token layer is a success worth copying.

---

## 3. Priority

| # | Finding | Area | Velocity cost | Effort |
|---|---|---|---|---|
| **D1** | Desktop shell is a copy of the mobile shell — 176 duplicated lines, every change costs 2× | Frontend | **Very high** | Medium |
| **D2** | Layout invariants encoded in prose + string-matched CSS; same bug recurs | Frontend / tests | **Very high** | Medium |
| **D3** | `PrimaryScreen.css` — 2825-line shared layout god-file, 64 bug-fix commits | Frontend | **High** | Medium |
| **D4** | `api.Server` optional-dependency pattern; `routes.go` at 1779 LOC (+69%) | Backend | **High** | Medium |
| **D5** | Composition root grows with every feature; two cycles closed by mutable late-binding | Backend | **High** | Medium |
| **D6** | God units keep growing; the written split plan was never executed | Backend | Medium | Medium |
| **D7** | `brain/tools.go` — adding one tool edits five regions of one file | Backend | Medium | Low |
| **D8** | `identity.Service` — five responsibilities, 40 methods, one type | Backend | Medium | Medium |
| **D9** | Card-kind taxonomy re-expressed in 8+ files (audit P8, now worse) | Frontend | Medium | **Low** |
| **D10** | Gate gaps: `schema-verify` unwired, codegen unpinned, 6 hand-run scripts outside the gate | Harness | Medium | **Low** |
| **D11** | 30% of history is merge commits; hot shared files are the collision surface | Process | Medium | — |

---

## 4. Findings

### D1 — The desktop shell is a hand-copy of the mobile shell *(highest cost)*

**Evidence.**
- `frontend/src/components/desktop/DesktopScreenView.tsx` (540 LOC) and
  `frontend/src/components/PrimaryScreenView.tsx` (616 LOC) share **176 distinct verbatim lines**.
  Not just idioms — whole module-level functions: `dividerIndex`, `findTicket`, `isSeen`,
  `updateId`, plus `EMPTY_SUMMARY`, the `openTicketId`/`ticketVoiceActive` state pair, the
  `firstOld`/`hasNewerAbove` seen-collapse computation, and the identical line
  `const isUpdate = card.kind === 'update' || card.kind === 'preview';`
  (`PrimaryScreenView.tsx:152`, `DesktopScreenView.tsx:115`).
- The desktop shell landed **four days ago** (`3d57f15`, 2026-08-04). In those four days: 139
  commits, 33 of them fixes, **14 commits touching both shells** and 25 desktop-only.
- The duplication is spreading, not shrinking: `KanbanScreenView.tsx` is now a *third* shell over
  the same data.

**Why it slows development.** Every feed behaviour — seen-collapse, the "Show earlier" divider,
ticket-open routing, toast handling — now has two independent implementations that must be kept in
lockstep by hand. The type checker cannot tell you that you updated one and not the other. A
four-day-old subsystem already needing 14 cross-shell commits is the leading indicator; at the
current rate this compounds every week.

The clearest proof is the "Show earlier" / toast-layering saga — the *same conceptual bug* fixed
five times over two days, alternating shells:

| Commit | Date | Shell |
|---|---|---|
| `039cdd9` "show earlier" sits at the foot of the feed | 08-06 | both |
| `4d9e4ab` the dock layer paints over the feed's pinned "Show earlier" | 08-06 | mobile |
| `b625786` the desk reserves the activity band, so a toast cannot cover it | 08-06 | desktop |
| `32cd8d7` a toast overlays the pinned "Show earlier", it does not push it up | 08-07 | mobile |
| `7e40588` the desk holds "Show earlier" still under a toast too | 08-07 | desktop |

Fix mobile → desktop breaks → fix desktop → mobile regresses. Five commits, two shells, one bug.

**Direction.** Extract the shell-independent half into one place and let the two shells be
*presentation only*:
- A `useFeedScreen()` hook (or equivalent) owning open-ticket state, the divider/seen computation,
  card routing, and voice-active state. Both `PrimaryScreenView` and `DesktopScreenView` consume
  it; neither re-derives it.
- Move `dividerIndex` / `findTicket` / `isSeen` / `updateId` into `feed-format.ts`, which already
  exists for exactly this purpose and is already imported by both.
- Treat "a helper defined in both shells" as a lint-visible defect, not a style preference.

This is the one item where the cost of *not* acting rises fastest, because the third shell
(`/kanban`) has already started.

---

### D2 — Layout invariants live in prose and in string-matched CSS

**Evidence.**
- `.agents/skills/web-client/SKILL.md` is **1209 lines** — 45% of all 2672 skill-doc lines — and is
  touched by **19 bug-fix commits**, making it the *second most fix-touched path in the repo* after
  `PrimaryScreen.css`. Four of the five "Show earlier" fixes above amended it.
- Its "Bottom-anchored UI layering (standing principle)" section is ~75 lines of prose describing an
  invariant — which surface anchors to which dynamic height, which `z-index` is sealed inside which
  stacking context — that no code expresses and no test enforces.
- 11 test files (**1945 LOC**) assert CSS by importing the stylesheet with `?raw` and slicing it
  with `indexOf`. `DesktopScreen.layout.test.ts` hand-rolls three near-identical `ruleBody()`
  parsers to read *four* stylesheets (`DesktopScreen.css`, `tokens.css`, `TicketDetail.css`,
  `PrimaryScreen.css`) and re-derives a magic constant in a comment:
  `MIC_GLOW_REACH_PX = 20` ("a 5px spread under a 28px blur… a blur fades over roughly half its
  radius").
- Six hand-run browser scripts live outside the gate and are referenced by neither the `Makefile`
  nor `tests/package.json`: `show-earlier-skirt-repro.mjs`, `toast-mic-glow-repro.mjs`,
  `desktop-shell-smoke.mjs`, `kanban-smoke.mjs`, `landing-auth-buttons-smoke.mjs`,
  `capture-landing-shots.mjs`. They rot on their own — `fix(tests): the hand-run layout scripts
  stub the GitHub App connection shape` (2026-08-06).

**Why it slows development.** The team has correctly diagnosed that jsdom performs no layout, and
has responded by (a) writing the rule down for the next agent and (b) asserting the *text* of the
CSS. Neither catches the bug. A `?raw` test passes when the string is present and the layout is
still broken — which is precisely what happened five times in a row. Meanwhile every fix pays a
tax: amend the prose, update the string assertions, and hand-run an `.mjs` script that the gate
will never run again. And the string tests are themselves fragile — reformatting a stylesheet
breaks them for reasons unrelated to behaviour.

**Direction.**
- Pick **one** real layout check and make it part of the gate: a small Playwright (or
  headless-Chrome) suite that renders both shells at two viewports and asserts *computed geometry* —
  "the toast band's box does not intersect the 'Show earlier' box", "the hub's bottom edge is above
  the dock's current top". That is a handful of assertions and it retires most of the 1945 LOC of
  string matching plus the six `.mjs` scripts.
- Make the layering rule structural where it can be: publish the named layers once (e.g. a single
  `@layer`/z-index scale in `tokens.css`, alongside the colour tokens that already work), so
  "hub 6 > transcript 5 > dock 1" is a lookup rather than a paragraph.
- Keep the skill doc for *intent*; stop using it as the enforcement mechanism. Prose that has been
  amended by 19 bug fixes is a missing abstraction with a changelog.

---

### D3 — `PrimaryScreen.css`: one 2825-line stylesheet is the repo's top bug site

**Evidence.** 2825 lines, 229 rule blocks, 217 comment lines, 4 media queries, 9 locally-declared
custom properties. **118 commits** (highest churn in the repo, 1.8× the next file) and **64 bug-fix
commits** (highest by a factor of 3.4). It is also imported *by the desktop layout test* to keep
cross-shell values in sync.

**Why it slows development.** It is the shared coordinate space for the dock, transcript, toast
hub, feed, header, status marks and the bottom reserve — so every UI ticket lands in the same file,
and every parallel agent collides there. Its size is the direct cause of the cross-shell coupling
in D1/D2: the desktop stylesheet cannot express its own layout without reading the mobile one.

**Direction.** Split by owned surface, following what the components already are — `dock.css`,
`activity-band.css`, `feed.css`, `screen-shell.css` — with the genuinely shared vocabulary
(the bottom-reserve variables, the layer scale, the status mark) promoted into `styles/`. The goal
is not smaller files for their own sake; it is that a dock change and a feed change stop being the
same merge conflict. Do this *with* D2, not before it — the layout tests are what will tell you the
split was safe.

---

### D4 — `api.Server`'s optional-dependency pattern

**Evidence.** `backend/internal/api/routes.go` is now **1779 LOC (+69% in one month)** with 50
commits. `api.Server` carries a 7-argument `NewServer` plus **15 `Enable*` setters**
(`EnableTicketSandbox`, `EnableTicketText`, `EnableDevTickets`, `EnableDevNotifications`,
`EnableReset`, `EnablePush`, `EnableBeta`, `EnableIdentity`, `EnableProviders`, `EnableTenancy`,
`EnableDevSession`, `EnableSandboxCatalog`, `EnableHealthz`, `EnableSPA` — 14 in `routes.go` — plus
`EnableCanonicalHost` in `canonical.go`), and **63 nil-comparisons** in `routes.go` alone. Each field's doc comment encodes a
mounting rule in prose: `// non-nil ⇒ POST /api/tickets/{id}/text is mounted`.

**Why it slows development.** The server has 2^15 nominal configurations of which only a few are
valid, and nothing checks which one you built. Ordering constraints are real but invisible —
`EnableDevSession` is documented as requiring auth to be enabled first; `EnableTenancy` and
`EnableSandboxCatalog` must be set together. A missed `Enable*` call does not fail to compile and
does not fail a test: the route silently 404s at runtime. Adding one endpoint means editing five
regions of a 1779-line file plus two files in `cmd/kiln`.

**Direction.** Move from "construct then progressively enable" to "declare a feature set, build
once": group the setters into cohesive capability structs (`IdentitySurface`, `TenancySurface`,
`DevSurface`, `PushSurface`) passed to a single constructor that returns an error for an incoherent
combination. Then split `routes.go` by surface — the file already has natural seams
(`identity_handlers.go` and `auth_handlers.go` were split out and are healthy at 420/414 LOC;
sandbox, feed, and dev routes are the obvious next three).

---

### D5 — The composition root grows with every feature, and two cycles are closed by mutation

**Evidence.**
- `wiring.go` 1118 LOC (+42%, **52 commits — the highest-churn backend file**) and `adapters.go`
  1137 LOC (+29%, 36 commits) — ~2.3k LOC of wiring holding **27 adapter types**.
- Much of it is mechanical `projectID` injection. `boardAPIAdapter` is eight one-line methods that
  do nothing but insert `a.projectID` into a call, each carrying an identical `//nolint:wrapcheck`
  comment; `boardReaderAdapter`, `sayAdapter`, `convoAdapter`, `notificationsAdapter`,
  `feedReaderAdapter` repeat the shape.
- Two construction cycles are still closed by mutable late-binding: `agentEvents.rt = rtSvc`
  (`wiring.go:270`, comment: "close the runtime↔agent cycle") and the registry closure over
  `&rtSvc`/`&agentSvc`. Reordering two lines produces a nil-deref at the first event, invisible to
  the compiler.
- Both files appear in 9 bug-fix commits each.

**Why it slows development.** The composition root is on the critical path of nearly every backend
ticket, so it is where parallel agents collide, and it is the least testable file in the repo. The
per-project adapter boilerplate means a new board operation costs an interface method, an adapter
method, a `nolint`, and a wiring line before any behaviour is written.

**Direction.**
- Replace the per-adapter `projectID` field with one generic project-scoping mechanism (a
  `ProjectScope` value threaded through context, or one generated/reflective scoping wrapper), so
  new operations stop requiring a new one-line adapter method.
- Replace the pointer late-binding with an explicit two-phase build that fails at construction
  (build the graph, then a `Link()` step that returns an error if a slot is unset) — the nil-deref
  becomes a startup error rather than a first-event runtime error.
- Split `wiring.go` by subsystem (identity/tenancy, board+agent, runtime+push, api surface). This
  alone removes most of the merge collisions.

---

### D6 — The god units keep growing, and the written plan was never executed

**Evidence.** `docs/god-units-plans/runtime-service.md` is a detailed, ten-step, keep-it-green split
plan for `runtime/service.go`, committed 2026-07-08 (`313db98`). It has never been acted on — the
only commits to that file since are six feature commits, and the file grew 840 → 941 LOC. The
14-port constructor with its "append new ports at the end" doc comment is still there.

**Why it slows development.** `runtime.Service` is simultaneously event dispatcher, outbox router,
transcript facade, feed assembler, notification CRUD and push coordinator. Any test of any one of
those constructs all fourteen ports. That is a fixed tax on every runtime ticket, and it is why
`fakes_test.go` is 1001 LOC.

**Direction.** The plan is good and still accurate; the problem is that it is one 10-step ticket
nobody picks up. **Re-scope it as six independent tickets** — steps 1–6 of that plan are already
written to each leave the tree green behind a delegating shim. Land `Notify` and `Feed` first (the
two smallest, ~2 ports each) to prove the shim approach, then the rest can proceed in parallel.
Deleting `Service` (steps 7–9) becomes a cleanup ticket rather than a prerequisite.

---

### D7 — `brain/tools.go`: adding one tool edits five regions of one file

**Evidence.** 1221 LOC (+17%), 21 commits, holding: 15 input structs (`:79-254`), four schema
helpers, the whole `Tools` table (`:278-457`), `Dispatch`/`dispatchOne`/`routeTool` (`:458-528`),
14 `do*` handlers, an embedded ticket-update state machine
(`validateUpdate`→`applyUpdate`→`applyStateStep`→`verifyDone*`→`applyState`, `:643-864`), and six
result formatters.

**Why it slows development.** Adding a tool means five separate edits in one file — input struct,
`Tools` entry, `routeTool` case, `do*` handler, formatter. Two agents adding two tools in parallel
conflict every time. The exact same conflict shape applies to the `dispatch_test.go` at 960 LOC.

**Direction.** The lowest-effort item in this report: split into `tool_schemas.go` (inputs +
`Tools`), `tool_dispatch.go` (routing), `tool_handlers.go` (the `do*` set), and pull the
update/verify state machine into its own `ticket_update.go`. No interface changes, no schema regen.

---

### D8 — `identity.Service` is five services

**Evidence.** 1234 LOC, 40 methods on one struct, 24 commits, 7 bug-fix commits. In one type:
GitHub OAuth *and* GitHub App install flow (`ConnectURL`, `InstallURL`, `CompleteConnect`,
`AttachInstallation`, `resolveInstallation`, `GitHubTokenSource`, `ListGitHubRepos`), session
management (`CreateSession`, `ResolveSession`, `Logout`), account (`EnsureUser`, `Me`,
`DevSignIn`), project/tenancy CRUD (`CreateProject`, `UpdateProject`, `UpsertProject`,
`ProjectByID`, `SoftDeleteProject`, `ProjectFor`, `GetProject`, `ListProjectIDs`, `RuntimeConfig`),
and credential verification (`SetVerifier`, `Verify`, `VerifyProject`, `verifyRepo`).

**Why it slows development.** This is where the recent sign-in instability lives — three fixes in
two days (`060b3ca` "signing in starts at authorize", `ef8d505` "sign-in happens on one origin",
`991e368` "a completed sign-in lands in the app") — because the redirect/state/cookie/landing
decisions are spread across `identity.Service`, `api/auth_handlers.go` and `api/identity_handlers.go`
with no single owner of "what a sign-in round trip looks like".

**Direction.** Split along the seams that already exist in the method list: `githubconnect`
(OAuth/App flow + token minting), `sessions`, `accounts`, `projects` (tenancy CRUD + `RuntimeConfig`),
`verification`. Do the *sign-in flow* one first and give it a single owner — that is where the bugs
actually are. Also fold in the audit's still-open low-hanging items here: the duplicate sentinels
`errIdentityNotConfigured` (`wiring.go:192`) and `errIdentityUnconfigured` (`wiring.go:642`) still
carry byte-identical messages.

---

### D9 — Card-kind taxonomy has no source of truth *(cheapest real fix)*

**Evidence.** The audit flagged this at 6 files; it is now **8+ production files** and has spread
into the new desktop shell: `feed-store.tsx:55,138,417`, `transport.ts:209-213`,
`PrimaryScreenView.tsx:152,165,265`, `DesktopScreenView.tsx:115`, `FeedCardItem.tsx:280-305,388`,
`feed-format.ts:172-178`, `desktop/backlog.ts:47`, plus `project-status.ts` and `project-cache.ts`.
Adding a card kind means finding every one of them; the type checker will not help, because these
are string comparisons against a union that permits all of them everywhere.

**Why it slows development.** Small in isolation, but it is the thing that makes *every* feed
feature a search-and-touch exercise, and it is the mechanism by which D1's duplication keeps
reproducing itself.

**Direction.** One `card-kinds.ts` exporting the predicates the codebase keeps re-inlining
(`isUpdateLike`, `isActionable`, `isClearable`, `needsTicket`) and a single exhaustive `switch`
helper so a new kind produces a compile error at every decision point. This is a few hours and it
pays back on every feed ticket thereafter. Do it as part of D1 — the extraction targets are the
same lines.

---

### D10 — Gate gaps that let avoidable breakage through

All four of the 2026-07-08 audit's "do first — cheap, high leverage" items are still open:

- **`schema-verify` is defined but unwired.** `Makefile:121` defines it; `Makefile:50` is
  `check: lint typecheck test`. It appears nowhere in `.github/workflows/` or `.githooks/`. A
  forgotten `make schema` passes CI silently — and schema-touching commits average **23.8 files**,
  so this is exactly the kind of change where a regen gets missed. **One line.**
- **Codegen is unpinned** — `oapi-codegen@latest` (`Makefile:30`) and `openapi-typescript: ^7.4.0`
  (`frontend/package.json:45`). Two agents regenerate divergent output. Combined with the above,
  "generated files never drift" is unenforceable.
- **CI is still advisory.** `.github/workflows/check.yml` says so in its own header comment: Render
  auto-deploys `main` regardless of the run. Local hooks are opt-in (`make hooks`) and bypassable.
- **Docs still describe a system that does not exist.** `backend/internal/api/doc.go:7-8` still
  reads "Auth on these endpoints is deferred… single-user; v1 is local-only" — in the package that
  now hosts OAuth, sessions and tenancy. An agent reading the package doc before working is being
  actively misled.

Plus, from this review: the six hand-run `.mjs` scripts in `tests/` (D2) are outside every gate and
already drifting.

---

### D11 — Integration overhead is structural, not incidental

**Evidence.** 243 of 797 commits are merges (**30.5%**); 51 are explicit "merge origin/main into
&lt;branch&gt;" integration merges. Weekly merge counts track commit counts almost 1:1 (W27: 77
merges / 256 commits; W28: 83 / 329; W32: 76 / 170).

**Why it slows development.** Parallel agents on `main` is a deliberate and reasonable choice — but
the collision surface is determined by how concentrated the hot files are. Every finding above is
also a merge-conflict finding: `PrimaryScreen.css` (118 commits), `wiring.go` (52), `routes.go`
(50), `PrimaryScreenView.tsx` (64), `transport.ts` (43). The W32 ratio (76 merges for 170 commits)
suggests roughly one integration merge per two commits of real work.

**Direction.** No process change recommended — the fix is D1/D3/D4/D5/D7. Splitting the five hot
files is what reduces the collision rate. Worth re-measuring this ratio afterwards as the
scoreboard for whether the rest of this plan worked.

---

## 5. Suggested sequencing

Deliberately front-loaded with things small enough to survive contact with a normal ticket — the
2026-07-08 audit's fate suggests that is the binding constraint, not correctness of analysis.

**Week 1 — cheap, unblocks measurement (≈1 day total)**
1. Wire `schema-verify` into `make check` (D10) — one line.
2. Pin `oapi-codegen` (tools.go / go.mod tool directive) and exact `openapi-typescript` (D10).
3. Fix `api/doc.go` and the duplicate identity sentinels (D10, D8).
4. Extract `card-kinds.ts` (D9) — a few hours, pays back immediately.
5. Split `brain/tools.go` into schemas / dispatch / handlers / ticket-update (D7) — mechanical, no
   interface or schema change.

**Weeks 2–3 — the frontend duplication, which is compounding fastest**
6. One real layout check in the gate (D2): computed-geometry assertions on both shells at two
   viewports. Retire the six `.mjs` scripts into it.
7. Extract the shared feed-screen logic into `feed-format.ts` + a `useFeedScreen()` hook; make both
   shells presentation-only (D1). Do this *after* 6, so the layout suite guards the move.
8. Split `PrimaryScreen.css` by owned surface, promoting the shared layer/reserve vocabulary into
   `styles/` next to `tokens.css` (D3).

**Weeks 3–5 — backend structure**
9. Re-scope `docs/god-units-plans/runtime-service.md` as six tickets; land `Notify` + `Feed` first
   (D6).
10. Two-phase composition-root build that fails at construction, and one generic project-scoping
    mechanism replacing the per-adapter `projectID` fields (D5).
11. Capability-struct constructor for `api.Server`; split `routes.go` by surface (D4).
12. Split `identity.Service`, starting with the sign-in flow (D8).

**Ongoing scoreboard.** Re-measure after each phase: merge-to-commit ratio (D11, currently ~0.45),
bug-fix commits per file for the top five hot files, and the file-count-per-feature-commit average
(currently 9.3 overall, 23.8 for schema-touching changes). If those do not move, the split was
cosmetic.

---

## 6. Two judgement calls worth flagging

- **The desktop shell duplication (D1) is the item I would act on first if only one thing gets
  done**, ahead of the cheap wins, because it is four days old and still cheap to undo. In three
  more weeks it will have accreted enough divergent behaviour that unifying the shells becomes a
  rewrite rather than an extraction. The Week-1 items are listed first only because they are
  near-free and can run in parallel.
- **I did not find a feedback-loop problem.** The gate runs in under a minute and passes. If the
  team's felt experience is that development is slow, the evidence says the time is going into
  *doing the same work twice* (D1), *fixing the same bug repeatedly* (D2), and *threading features
  through wide seams* (D4/D5) — not into waiting.

---

*Nothing in this report was implemented. Sizes and churn figures are reproducible from
`git log`/`git show` at the commit boundaries named above; gate timings were measured on
2026-08-08 at `f4f9b28`.*
