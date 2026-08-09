---
name: runtime-and-api
description: Use when working in the runtime / event-queue / client-facing API — the durable, deploy-resumable service shell that ingests events, drives the brain once per event, and faces the client. Also the home of identity/tenancy wiring and the leader lock. Backend anchors internal/runtime and internal/api. Spec docs/specs/04-runtime-and-api.md.
---

# Orchestrator API + event queue / runtime (mechanics decided by spec 04)

## What it is

The durable, deploy-resumable service shell that receives events, drives the brain once per
event, and faces the client. It implements the `01` decision that the orchestrator **wakes on
events, not a timer**. Two event types reach it: `agent.turn_completed` (from the agent-runtime
module, 05) and `human.message` (from `POST /api/message`; voice feeds the same seam).

Settled mechanics (04): at-least-once delivery, execute-then-mark, backoff
`min(1s × 2^(attempts−1), 60s)` over 8 attempts, per-topic dead-letter actions; a **single
serial event-worker goroutine** in `id` order; no recovery code path at all — a restart just
re-finds `pending` rows, woken by a nudge channel with a 1 s poll fallback; **SSE
(server→client) + plain HTTP POST (client→server)**, absolute snapshots, reconnect = fresh
snapshot, no replay. Both event types share the `events` table and serialize by insertion
order; the outbox drains on its own serial worker.

Every contract is in the packages' doc comments — read those for shapes. `internal/runtime` is
mid-split (`docs/god-units-plans/runtime-service.md`): `Service` is now a thin aggregate over
six focused units (Dispatcher, Transcript, Notifications, Feed, Notify, FanOut) that forwards
every method in one line, kept only until steps 7–9 flip `cmd/kiln` to the six values and
delete it. **Add nothing to `service.go`** — add it to the unit that owns the responsibility.

Two orientation points that are easy to get backwards:
- **`dispatcher.go` is the spine** — an at-least-once drain where a returned error retries and
  then dead-letters. It routes nine outbox topics; the three `agent.*` ones go together through
  `routeAgent`. `agent.snapshot` is the newest and the one exception to `AgentRuntime`'s
  record-and-return rule: a saved sandbox's exit from Developing captures the workspace rather
  than recycling it, and its executor calls the provider inline, leaning on this queue's own
  retry/dead-letter policy for durability because there is no turn to progress afterwards.
- **`fanout.go` is its exact inverse** — the runtime's one log-and-drop file, because
  everything it emits is a *view* of already-durable state. `HandleFeedCompletion` is the
  exception (a persistent card), so its failures are returned.

## The route surface

`GET /api/stream` carries **four** named SSE events — `board`, `say`, `feed`, `activity`. The
rest of the surface: board/messages, the feed group, activity, the ticket-action routes (below),
voice token, the push group, the identity group, dev routes (gated), the SPA catch-all, and
**`GET /healthz`** (liveness + DB ping, 200 ok / 503 degraded) mounted **outside `/api`**.

**Every `/api/*` handler is wrapped in `withProject`** (11 phase 2) — session-authenticated and
project-scoped before it runs. Only the public marketing/onboarding routes and `/healthz` sit
outside the gate. Some routes are dual-mounted via `mountProjectScoped` (bare = the caller's
first project, `/api/projects/{pid}/…` = a named project, 12 §3.2).

## The direct board writes (the D5 exceptions)

Everything the client does to the *board* goes through the brain — Accept and Delete are
synthesized human messages, not mutations. **Three** ports break that, each optional so the
exception stays visible in the type surface rather than hiding as a method on `BoardReader`:

| Port | Routes | Board op | Why it skips the brain |
| --- | --- | --- | --- |
| `TicketSandboxController` | `POST /api/tickets/{id}/sandbox`, `…/sandbox/kill`, `…/sandbox/reassign` | `SetKeepSandbox`, `KillSandbox`, `ReassignSandbox` | The toggle is a setting, not a transition. The two overrides exist so the user can reach *past* the orchestrator when a sandbox is wedged — an override that waits on an LLM turn is not an override. |
| `TicketTextEditor` | `POST /api/tickets/{id}/text` | `ShapeTicket` | An LLM pass is the *thing being avoided*: dictating a wording change and letting the brain rewrite the ticket is what drifts from what the user meant. |
| `TicketDependencyController` | `POST`/`DELETE /api/tickets/{id}/dependencies` | `AddDependency`/`RemoveDependency` | A dependency is a per-ticket setting that moves no ticket between states, so there is no decision for the brain to own. |

The three sandbox routes share **one** port and one `Enable…` because they are one surface, so
a deployment can't end up with a kill route over a nil setter. **A fourth "just this once"
write is how D5 stops meaning anything.** If you add one, it needs its own port, its own
`Enable…`, and a reason in the port's doc comment that is about *this* operation, not about
convenience.

**These handlers map board preconditions to 409, never 500** — they are states the user can act
on: `*board.ErrInvalidTransition` (no worker bound, or a ticket past the backlog for a text
edit), `board.ErrNoFreeWorker` on reassign (nowhere to move to), `*board.ErrCircularDependency`
on add (and its message names both ends, so the sheet can say what it collided with). The
sandbox routes take **no request body** — the ticket in the path is the whole request. The
text route's body-level rejections run *before* the write, because an empty patch would still
fan a `board.updated` out to every open client and a nameless ticket has no identity on the
board or in the feed: **an empty body is a legal edit; an empty title is not.**

## Identity + tenancy (spec 11)

A sibling module (`internal/identity`), not part of runtime/board — `api` consumes it through
two ports (`Authenticator`, `AccountService`), same pattern as `BoardReader`. Phase 1 added
the account surface; phase 2 made the whole app project-scoped.

- **Env gating is all-or-nothing.** Identity needs **all six** of `KILN_GITHUB_APP_ID`,
  `_SLUG`, `_PRIVATE_KEY`, `_CLIENT_ID`, `_CLIENT_SECRET` and `KILN_SECRETS_KEY` — the list
  lives once, in `identityEnvVars`. Any missing → `EnableIdentity` is never called, so
  `/auth/*` and `/api/me` are simply **absent** (404, not 401). All absent is silent: that is
  a deployment that never turned identity on. A malformed `KILN_SECRETS_KEY` or App private
  key fails the boot **hard**, rather than silently running with broken crypto or a connect
  flow whose every mint fails.
- **The App private key is base64-encoded.** GitHub emits a multi-line PEM and multi-line
  secrets do not survive a hosting provider's environment; it is decoded once at boot (a raw
  PEM is accepted too).
- **Write-only secrets.** `PUT /api/settings` accepts raw values; `GET /api/me` only ever
  returns `{set, tail}` per secret. The plaintext never round-trips over the wire.
- **Dev session mint.** `POST /api/dev/session` (gated by `KILN_DEV_ENDPOINTS=1` **and**
  identity enabled) signs in — or creates — a user from a plain `{github_login}` and mints a
  real session cookie. This is how e2e establishes a session; never part of `/schema`, never
  mounted without dev endpoints on.

### Login runs through a GitHub App

(`docs/superpowers/specs/2026-08-04-github-app-repo-selection-design.md`; the OAuth App it
replaced is gone as of 2026-08-06, and every session was invalidated by migration rather than
migrated.) The user picks on GitHub's own chooser which repos Kiln may reach. Six things to
know before touching it:

- **Sign-in starts at `authorize`, not at the install page.** `installations/new` completes
  exactly once per account; after that GitHub answers it with the installation's *configure*
  page and never calls the callback, which stranded every returning sign-in on github.com. So
  the callback resolves the installation itself: `installation_id` when the callback carries
  one, else `ListUserInstallations` filtered to this App, else — and only else — a one-hop
  redirect to the install page. **A *failed* listing keeps the stored installation**;
  unreachable is not uninstalled. `?setup=1` asks for the chooser deliberately.
- **The credential is a function, not a value.** `identity.TokenSource` resolves per git/`gh`
  invocation because an installation token dies within the hour. **Never hoist a resolved
  token into a longer-lived variable.**
- **The adapter is stateless; the cache belongs in `identity`.** A mint is a network call;
  `InstallationTokens` caches until `refreshMargin` before expiry, with a per-installation
  singleflight so a waking fleet costs one round trip.
- **A rejected mint is recorded, not just returned.** `ErrInstallationUnavailable` (401/403/404)
  flips the card to `needs_reconnect`. **A transport failure must NOT** — a network blip is not
  a revoked grant. This is also why `GET /api/me` stays a pure DB read: what GitHub thinks is
  learned when a credential is *used* and written down for the read to find.
- **`ListInstallationRepos` takes a USER token, not the minted one.** Deliberate: the
  installation-wide listing would offer an org member repos they cannot themselves reach.
- **A stored token with no installation still works** — a hand-typed PAT or the deployment's
  bootstrap token. It reads as `unknown` (stored, not granted by Kiln), and writing one clears
  any installation so the card never claims a connection the runtime is not using.

**The callback has two shapes.** `code` is the full identity path, state-checked.
`installation_id` alone — someone installing from GitHub's own Apps page, who never passed
through connect and so has no state cookie — attaches to the existing session instead. The
install hop is bounded by a marker cookie: a declined install looks exactly like never having
been offered one, so without it the browser would ping-pong forever.

**A completed sign-in lands in the app**, not on `/dashboard` (which is where it used to land
unconditionally). Two exceptions: a caller with no project (onboarding lives on `/dashboard`),
and one that ASKED for the dashboard because the affordance lives there. The request has to
survive a round trip through GitHub, so it rides in the **state nonce** rather than a second
cookie: GitHub echoes state back verbatim and the callback has already compared it against the
cookie before reading it. **`next` is a closed set of names, never a URL** — an open redirect
on this route is the last thing anyone wants. The bug it fixes reads as desktop-only and is
not: a phone's installed web app relaunches at `start_url` and finds `/app` on its own, so
only a browser tab ever sat on the wrong screen.

**Sign-in needs ONE public origin (`KILN_PUBLIC_URL`, `api/canonical.go`).** A cookie belongs
to a host; the App's callback URL is a fixed string in GitHub's settings and has no idea which
host the user started on. A deployment answering on both its platform hostname and its real
domain splits the flow across two cookie jars — state written on one, read on the other, found
on neither, which is exactly the "missing oauth state cookie" 400 users hit; and had it passed
they'd have been handed a session cookie on the wrong domain. `EnableCanonicalHost` redirects
any off-origin GET/HEAD onto the pinned origin **before the mux**. Two exemptions, both
load-bearing: `/healthz` (the platform's probe reaches an internal hostname; a 302 reads as
unhealthy) and non-safe methods (a redirected POST can lose its body) — and *nothing else*.
Unset ⇒ no wrapper at all (local/dev); **set-but-malformed ⇒ refuse the boot**, since a
shrugged-off typo serves the precise breakage the setting exists to close.

## The background loops are single-owner (`internal/leader`)

Render's zero-downtime deploy runs the old and new instance side by side for **67–83 s of every
deploy**, and both used to run the full set of background loops — so both polled for the same
pending work and both could start an agent against the same sandbox and working tree. Five
investigations (`docs/root-cause-2026-08-02-*` … `-08-04-part5-*`) confirmed it; the fix landed
2026-08-04.

- `graph.startLoops` runs the **four** loops — events worker, outbox worker, agent, steward —
  inside `leader.Elector.Run`. **Anything else that polls or sweeps on a timer belongs inside
  `backgroundLoops`, not beside it.**
- **The HTTP server is deliberately NOT gated.** A follower serves board/SSE/API normally; it
  just does no background work. Its writes still land (events queue up) and the leader drains
  them.
- The lock is `pg_try_advisory_lock` on a **pinned `*sql.Conn`**, not the pool: a lock taken on
  one pooled connection and released on another is a silent no-op. The Elector unlocks *before*
  returning the conn, and re-verifies via `pg_locks` every 5 s that its own backend still holds
  it — acquiring once at boot is not enough, because a dead connection drops the lock silently.
- Handoff is fast because a session-scoped lock dies with the session: measured **5 ms** to
  release on SIGTERM and **53 ms** end-to-end on SIGKILL. No lease, no heartbeat, no migration.
- Every record carries `instance`, so a handoff is legible across two instances' interleaved
  streams. Watch `leader.acquired` / `.standby` / `.released` / `.lost`.
- `KILN_LEADER_LOCK=0` restores the pre-lock behaviour — every instance polls, which *is* the
  collision. **Never set it to make a test pass.**
- **A leader lock is not a claim.** `stepStartTurn` still has no CAS
  (`docs/ticket-draft-turn-claim-cas.md`) and a fresh event row still gets a 1 s visibility
  lease (`docs/ticket-draft-queue-visibility-timeout.md`). Both remain real single-instance
  bugs; the lock does not subsume either.

## How to work here

- Build/check from `/backend`: `gofmt -l . && go vet ./... && go build ./...`.
- The runtime consumes the board through the narrow `Puller`/`Blocker` ports it names, not
  `*board.Service` directly; adapt at the composition root (services depend on ports).
- Unit-test the `Worker` against fake `Store`/`Handler`s with the `Clock` interface — **the
  backoff schedule must be testable without sleeping** (04 §9).
- The runtime owns the persisted transcript: append the user row and enqueue the event in one
  transaction; `Say` appends the kiln row and pushes over SSE; `ConversationReader` feeds the
  brain's context.

## Common footguns

- **Starting a new background loop with a bare `go …`.** It would run on every instance,
  re-opening the duplicate-work window the leader lock closes. Add it to
  `graph.backgroundLoops` instead.
- Adding a method to `runtime.Service` instead of the unit that owns the responsibility.

## Potential gotchas

- **Session-scoped advisory locks need direct Postgres connections.** Behind a
  transaction-pooling proxy (PgBouncer in transaction mode) they are meaningless, the
  Elector's periodic re-verification fails, and *no* instance leads — the symptom is "nothing
  ever starts working" rather than an error. Render's managed Postgres is a direct connection.
- **`leader.LockKey`'s high 32 bits must stay below 2^31.** Postgres stores a one-argument
  advisory key split across `classid`/`objid`, and the re-verification reassembles it with
  `(classid::bigint << 32) | objid::bigint`, which overflows above that.
  `TestLockKeyFitsPgLocksReassembly` guards it.
