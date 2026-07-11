# Done card work summary — design

**Date:** 2026-07-11
**Status:** Proposed (design only — no implementation in this change)
**Scope:** `schema/openapi.yaml`, `internal/repo`, `internal/brain`,
`internal/board`, `internal/runtime`, `internal/api`, `frontend` (the done feed
card). One `notifications` migration. Builds directly on the done card and its
GitHub link (commits `f012f5d`, `cf8930c`, `6c009d4`).

## Problem

When a ticket lands, the feed posts a **done card** (08 §7): a body-less
`✅ <ticket title>` with a second line linking to the landed work on GitHub —
the abbreviated commit SHA under the merge-on-main gate, or `#<number>` under the
PR gate (`FeedCardItem.tsx:326`). The card answers *"which ticket finished"* and
*"where did it land"* but not *"**what** actually landed"*.

To learn what the agent actually did, the user must leave the feed: tap through
to GitHub and read the commit or PR. That is the one question a completion notice
should answer inline. The ticket title states the *intent the user filed*; it does
not describe the *change that shipped* — which is often more specific
(conventional-commit scoped, e.g. `feat(web): show a 404 page for unmatched
routes`) and is the artifact worth a glance to confirm the work matches the ask.

**Objective:** surface the commit message or PR description on the done card so a
user understands the work without leaving the feed.

## What already exists (the pipeline to extend)

The done card's GitHub link already flows end to end through a fully mechanical
path. The work summary rides the *same* path — every hop already carries
completion metadata; we add one field per hop.

| Hop | Location | Carries today | Add |
| --- | --- | --- | --- |
| 1. Repo verify | `repo.Verify` (`repo/repo.go:165`), filled in `verifyOnMain:244` / `verifyInPR:267` | `URL`, `Ref` | `Summary` (+ optional `Body`) |
| 2. Brain port | `brain.RepoVerify` (`brain/types.go:165`) | `URL`, `Ref` | mirror `Summary` |
| 3. Completion link | `board.CompletionLink` (`board/service.go:258`), filled in `verifyDoneOnMain:727` / `verifyDoneInPR:752` | `URL`, `Label` | `Summary` |
| 4. Outbox payload | `board.CompletionPayload` (`board/outbox.go:109`), emitted by `AcceptToDone:305` | `github_url`, `github_label` | `summary` |
| 5. Persistence | `notifications` table (migration `0009_notifications_github_link.sql`) | `github_url`, `github_label` | `work_summary` column |
| 6. Runtime card | `runtime.FeedCard` (`runtime/feed.go:55`) | `GitHubURL`, `GitHubLabel` | `WorkSummary` |
| 7. Wire | `FeedCard` (`schema/openapi.yaml:763`) → regen Go + TS | `github_url`, `github_label` | `work_summary` |
| 8. Client | `FeedCardItem.tsx:337` (body suppressed for done) | head + github line | render body |

The link is derived *once*, at the moment the brain's merge gate verifies the
commit (`brain/tools.go:691 verifyDone`), from the repo check it already runs. The
summary is sourced at that same moment from that same check — no new pass, no new
brain tool, no agent-side convention.

## Sourcing: commit message vs PR description

The central decision. The source is **gate-aligned** — it follows the project's
configured merge gate (`GateMode`, 06 §7), which already decides *which artifact
is authoritative* and *which artifact the card links to*:

- **`main` gate (default).** The commit on `origin/main` is the verified unit of
  work and what the card links to. Source the **commit subject** — the first line
  of the commit message (`git show -s --format=%s <sha>`).
- **`pr` gate.** The pull request is the unit of work (its commit may be one of
  several on a branch) and what the card links to. Source the **PR title**
  (`.title` from the `gh` call already made in `verifyInPR`).

**Why gate-aligned rather than "prefer PR description, else commit":**

1. **Single source of truth.** The summary always describes the exact artifact the
   card already links to. No divergence between the link target and the text.
2. **No extra network call.** Under `main` the summary is a local `git show` on a
   commit already fetched and `rev-parse`-verified; under `pr` it is two more
   fields on the `gh api .../pulls` call `verifyInPR` already makes. Nothing new
   is fetched.
3. **Feed hygiene.** PR *bodies* are frequently long and templated (checklists,
   "Generated with…" trailers). A PR *title* is the human one-liner, symmetric
   with a commit subject. Taking the title (not the body) by default keeps the
   feed scannable; the full description stays one tap away on GitHub via the
   existing link.

### Content hygiene

- **Subject only, by default.** `%s` yields just the subject, so commit trailers
  (`Co-Authored-By: …`) never reach the card.
- **Optional full body (deferred, see below).** If we later expand the commit
  *body* inline, strip trailer lines and collapse blank runs before it ships.
- **Fail-soft.** `work_summary` is nullable at every hop. When it is absent — an
  older card, a repo check that could not read the subject, a PR with an empty
  title — the card renders exactly as it does today: `✅ <title>` + GitHub line.
  This degrades identically to how `github_url` already degrades when a link is
  unavailable, so no new empty state is introduced.

## UI / UX

The done card gains a **body line between the head and the GitHub link**, reusing
the card body treatment every other kind already uses — no new component, no new
interaction vocabulary.

```
┌────────────────────────────────────────────┐
│ ✅  Show a 404 page for unmatched routes  2m │   ← head: ✅ + ticket title + age
│                                              │
│ feat(web): show a 404 page for unmatched     │   ← body: commit subject / PR title
│ routes                                       │      (clamped to 3 lines, expandable)
│                                              │
│  the GitHub mark  a1b2c3d                    │   ← footer: existing GitHub link
└────────────────────────────────────────────┘
```

- **Layout.** `head` (`✅` + ticket title, already the ticket-detail tap target on
  a tagged card — `FeedCardItem.tsx:283`) → **new body** → `github` footer link.
  The head-tap-opens-ticket and footer-tap-opens-GitHub affordances are unchanged;
  the body slots between them.
- **Reuse `FeedCardBody`.** The body is the same expand-in-place paragraph
  update/blocker/preview cards use (`FeedCardItem.tsx:140`): clamped to three
  lines, and when the subject/message overflows it wears the shared *"tap to see
  more"* cue and toggles open in place. A one-line subject that fits stays inert
  plain copy — no affordance where there's nothing to expand. This matches the
  established rule that *a brain update is a self-contained note that expands in
  place, never a shortcut into a ticket* — a completion summary is the same shape.
- **The one change at the call site.** `FeedCardItem.tsx:337` currently suppresses
  the body for done and poke cards (`!isPoke && !isDone`). Relax it so a done card
  **with** a `work_summary` renders a `FeedCardBody`; poke stays body-less, and a
  done card **without** a summary stays body-less exactly as today.
- **Styling.** The body inherits the existing `feed-card-body` rules
  (`PrimaryScreen.css:930`); the GitHub footer already sits `align-self:
  flex-start` below it. Expect little to no new CSS — at most spacing between the
  new body and the footer line.
- **Seen state.** The done body collapses tighter below the last-seen divider via
  the existing `data-seen` rule (`PrimaryScreen.css:946`), like every other body.

### Why show the summary when the ticket title is right above it

They answer different questions and the delta is the point. The **title** is the
intent the user filed; the **summary** is what the agent actually shipped. Seeing
both inline lets a user confirm at a glance that the landed change matches the ask
— the exact judgement that today requires a trip to GitHub. When they happen to
read the same, the cost is one muted line; when they diverge, the card just earned
its keep.

## Data model & wire

**Migration** (mirrors `0009_notifications_github_link.sql`):

```sql
-- Work summary on the mechanical "done" completion card (08 §7): the one-line
-- description of the landed work — the commit subject under the main gate, or the
-- pull-request title under the PR gate — so the card says WHAT shipped without a
-- trip to GitHub. NULL on every other kind, and on a completion card whose summary
-- could not be read.
ALTER TABLE notifications ADD COLUMN work_summary text NULL;
```

**Wire** (`schema/openapi.yaml` `FeedCard`, after `github_label`):

```yaml
        work_summary:
          type: string
          nullable: true
          description: >
            Set for done cards — the one-line description of the landed work: the
            commit subject under the main merge gate, or the pull-request title
            under the PR gate. Rendered as the card body (08 §7). Null when
            unavailable.
```

Then **regenerate both sides** per the wire-schema rule — never hand-edit
`backend/internal/wire/generated.go` or `frontend/src/schema/generated.ts`; change
the schema and regenerate. The api-layer mapping (`runtime.FeedCard` →
`wire.FeedCard`) copies the new field alongside `GitHubURL`/`GitHubLabel`.

## Sourcing detail (backend)

- **`main` gate** — in `repo.verifyOnMain` (`repo/repo.go:244`), after the
  `merge-base --is-ancestor` check passes, add one local call
  `git show -s --format=%s <sha>` and set `Verify.Summary`. The commit is already
  fetched and verified, so this reads the local object graph — no network, no new
  failure mode (an unexpected non-zero exit just leaves `Summary` empty →
  fail-soft body-less card).
- **`pr` gate** — in `repo.verifyInPR` (`repo/repo.go:267`), extend the existing
  `--jq` program on the `gh api repos/{owner}/{repo}/commits/<sha>/pulls` call to
  also emit `.[0].title`, tab-separated after the number and URL. No extra request;
  parse the third field into `Verify.Summary`.

`brain.verifyDone` (`brain/tools.go:691`) already funnels both modes into a
`board.CompletionLink`; it copies `v.Summary` into the new `CompletionLink.Summary`
exactly where it copies `v.URL`/`v.Ref` today. `AcceptToDone` (`board/service.go:274`)
carries it onto `CompletionPayload`, and the runtime's completion consumer persists
it to `notifications.work_summary` and renders it on the feed card — all mirroring
the GitHub-link plumbing already in place.

## Rationale summary

| Decision | Choice | Why |
| --- | --- | --- |
| Source | Gate-aligned (commit subject on `main`, PR title on `pr`) | Matches the already-linked authoritative artifact; no dual fetch, no divergence |
| Granularity | One-line subject/title, not full body | Feed hygiene — PR bodies are long/templated; full text is one tap away on GitHub |
| Derivation point | The brain's existing merge-gate verify | The check already runs and already reads the commit/PR; no new pass or tool |
| Transport | Extend the existing done-link pipeline | Every hop already carries completion metadata; one field per hop, one migration |
| Rendering | Reuse `FeedCardBody` (expand-in-place) | No new component or interaction; consistent with every other card body |
| Missing summary | Nullable everywhere → today's body-less card | Fail-soft, backward compatible, degrades like the GitHub link already does |

## Out of scope / deferred

- **Full commit/PR body inline.** Expanding beyond the subject/title (with trailer
  stripping and blank-run collapse). The `Body` field is sketched at hop 1 but not
  wired; the subject/title covers the objective, and the full text is one tap away.
- **Rich formatting.** Markdown, code fences, issue-link autolinking in the body.
  The feed body is plain clamped text; keep it so.
- **Multi-commit PRs / squash context.** Listing every commit in a PR. The PR
  title is the unit-of-work summary; per-commit detail lives on GitHub.
- **Backfilling** existing done cards. The column is nullable; historical cards
  stay body-less. No migration of past rows.

## Testing (three-level gate — see end-to-end-development)

- **Unit.** `repo` — `verifyOnMain` sets `Summary` from an injected `git show`
  runner; `verifyInPR` parses the extra tab-delimited `title` field; both leave it
  empty on a non-zero exit. Board/runtime mapping copies the field through the
  outbox → notification → feed card.
- **Component/snapshot.** `FeedCardItem` — a done card **with** `work_summary`
  renders a `feed-card-body`; **without** stays body-less; a poke card is never
  given a body; the overflow → *"tap to see more"* cue toggles like other bodies.
- **End-to-end.** A ticket accepted to done surfaces a done card whose body shows
  the landed commit subject and whose footer still links to GitHub.
