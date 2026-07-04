# Kiln — Infrastructure & Operations (v1.5)

**Date:** 2026-07-04
**Status:** Proposed
**Scope:** single project, single user, single developer + many agents
**Relationship to `01`–`09`:** Lifts `02` §1's deliberate non-goal ("no production or
cloud-hosting decisions") and fills the `02` §3 hosting row that was pinned to
"local only — Docker Compose." Nothing in the module topology (`02` §2), the wire
contract (`02` §3), or the hard gate (`02` §4) changes; this spec decides where the
existing deployables run, how they get there, and how the system is observed and
operated once they do. Numbered `09` alongside `09-voice-pipeline` following the
duplicate-`08` precedent.

## 1. Purpose & scope

This document decides:

- The **hosting provider and production topology** (§3).
- **CI and continuous deployment** — the missing half of `02` §4's "CI runs the full
  hard gate" promise (no workflow exists today) plus deploy-on-main (§4).
- **Migrations and rollback** discipline in production (§5).
- The **observability stack**: errors, logs, metrics/traces, uptime (§6).
- **Alerting and the agent incident loop** — how a production problem reaches an
  agent, and what that agent is allowed to do about it (§7).
- The **agent access model**: which credentials exist, what each can touch (§8).
- **Secrets** (§9) and **backups** (§10).
- **Rollout phases** and how each is verified (§11–§12).

**The design constraint that drives every choice:** Kiln is maintained by one
developer and many coding agents, and the agents must be able to *operate* the
infrastructure — deploy, diagnose, restart, roll back — not just write application
code. So every surface below is scored the way `02` §3 scored languages: maximize
what a machine can do reliably. Concretely:

- **Everything as code.** Provider topology, CI, alert probes, backup jobs, and
  runbooks are files in this repo. An agent change to infrastructure is a git diff —
  reviewable, revertable, attributable. This is the single highest-leverage guardrail
  and it costs nothing.
- **Every operation is a non-interactive CLI/API verb.** If an action needs a
  dashboard click, it is not agent-operable and we design around it.
- **Read everything, write scoped.** Agents get broad read access (logs, metrics,
  errors, status) because reads are cheap and diagnosis needs them; writes are a short
  allowlist of verbs (deploy, restart, roll back) behind scoped credentials; anything
  destructive (schema, data, secrets, delete-service) requires the human.
- **Boring and managed beats cheap and DIY.** One user; the interesting problems are
  in the product. Managed Postgres with point-in-time recovery deletes an entire class
  of dangerous ops work no agent should be doing.

**Non-goals:** multi-region, autoscaling, staging environments, Kubernetes,
Terraform-grade IaC, SLOs/on-call process, and multi-user anything (`01` §10). v1.5
is one region, one instance of each deployable, deploys straight from `main`
(per `AGENTS.md`: no other users exist, backward compatibility is a non-goal).

## 2. Shape of the decision

Research (July 2026, current pricing and docs) compared Fly.io, Railway, Render,
Hetzner+Kamal, and newcomers against the agent-operability rubric: config-as-code,
non-interactive CLI, token scoping, log/metric API access, and managed Postgres at
hobby cost. Render wins on the criterion this spec weights first; the full comparison
is in the decision log (§13, D1). Observability is a $0/month stack chosen for the
same reason: every signal must be queryable by an agent through an official,
un-paywalled API or MCP server (§13, D4).

## 3. Hosting: Render

**Three Render resources, all declared in one repo file — `render.yaml`:**

| Resource | Maps to | Plan / cost |
| --- | --- | --- |
| Web service (Docker) | `/backend` Go binary, built from `backend/Dockerfile` | Starter, $7/mo |
| Managed Postgres 16 | the single `02` §3 state+queue store | Basic, $6/mo — daily backups + point-in-time recovery |
| Static site | `/frontend` production build (`pnpm run build` → `dist/`) | Free |

- **The blueprint is the topology.** `render.yaml` declares all three resources, their
  env vars, the health check path, and the pre-deploy command. Provisioning changes are
  edits to that file. No dashboard-built resources.
- **Same-origin holds in production** the same way it does under the Vite dev proxy
  (`02` §2): the static site gets a rewrite rule `/api/* → <backend service URL>/api/*`,
  so the client keeps calling relative `/api/...` and the credential boundary is
  unchanged. **Must be verified for SSE** (`04`'s stream) before phase 2 is called done;
  the fallback — serving `frontend/dist` from the Go binary via `embed.FS` — collapses
  us to one deployable and is acceptable (§13, D2).
- **A `/healthz` endpoint is added to the backend** (none exists today): `200` when the
  process is up and the DB pool answers `SELECT 1`. It is Render's health check (deploys
  don't take traffic until it passes), the uptime probe target (§6), and the first thing
  an investigating agent curls.
- **Region:** nearest Render region to the user; single instance of each service. No
  scale-to-zero — at $7/mo always-on, cold starts aren't worth their failure modes.
- **Docker Compose remains the local truth.** Nothing about `make up` changes; Render
  runs the same backend image built by the same Dockerfile.

**Agent operability on Render** (why it won — §13, D1): the whole stack in one repo
file; a GA non-interactive CLI (`render deploys create/list`, `render logs`,
`render restarts`, rollback, one-off jobs, `render psql`) with `-o json` output; a
first-party MCP server for logs/deploys/Postgres; managed PG at $6 with PITR. Its one
real weakness — API keys are workspace-broad, not per-verb — is mitigated by keeping
Kiln in a **dedicated Render workspace containing nothing else**, so the blast radius
of any leaked or misused key is exactly this hobby project (§8).

## 4. CI + CD: GitHub Actions

Two workflow files, both in `.github/workflows/`, both agent-editable code.

**`ci.yml` — the gate, then deploy.** On every push to `main` (and any PR, if one is
ever opened):

1. **Gate job** — exactly the hard gate the repo already defines, no CI-special logic:
   `make check` (lint → type-check/build → unit + integration) plus `make schema-verify`
   (`02` §4's wire-contract staleness wall). Integration tests get a Postgres 16 service
   container with `TEST_DATABASE_URL` set, matching the existing test convention. Go
   module/build caches keyed on `backend/go.sum`; pnpm store cache keyed on
   `frontend/pnpm-lock.yaml`.
2. **Deploy job** — `needs: gate`, `main` only. Render **auto-deploy is off**; CI is the
   only deploy trigger, so nothing reaches production without the gate. The job triggers
   both deploys via the Render CLI/API (backend image build + static site build) and
   waits for the health check to pass.
3. **Concurrency:** `group: deploy-production, cancel-in-progress: false` — deploys
   queue and are never killed mid-flight; GitHub's default queue keeps only the newest
   pending run, i.e. "deploy the latest main," which is what a solo-dev repo wants.
   (CI-only runs on non-main refs cancel freely.)

**The e2e suite stays out of the deploy gate.** `make e2e` hits real services (LLM,
Amika, STT) — real money, real flake, real sandboxes to clean up (`02` §1). It runs on
a nightly `schedule` and on `workflow_dispatch`, never as a deploy blocker. A nightly
red e2e is an alert (§7), not a wall. The gate's unit + integration coverage is the
deploy wall, exactly as `02` §4a scoped it.

**`sentinel.yml` — probe, and wake an agent on failure.** Described in §7; it lives
beside `ci.yml` because it is the same kind of artifact: operations as a reviewable
workflow file.

## 5. Migrations & rollback

- **Keep the existing migrator.** The backend already embeds each module's SQL
  migrations via `embed.FS` and applies them at startup under a `schema_migrations`
  ledger (`cmd/kiln/wiring.go`). That is the right shape for a single-instance deploy;
  no goose/golang-migrate/Atlas adoption (§13, D3). Startup application means a deploy
  with a bad migration fails its health check and never takes traffic.
- **Forward-only, expand → migrate → contract.** No down-migrations in production.
  Every migration must be compatible with the previous binary: additive change first,
  deploy code that uses it, remove old structures in a later migration. This is the
  rule that makes rollback always safe.
- **Rollback = redeploy the previous image.** One Render CLI verb, no rebuild, no
  checkout — deliberately a single allowlistable command so an agent can execute it
  (§7). Because schema never rolls back, previous-image redeploy is always compatible.
  The exact command is documented in the `production-ops` skill (§8).

## 6. Observability

$0/month; chosen tool-by-tool for agent queryability (official API/MCP, no paywall on
the free tier). One pick per signal, nothing overlapping:

| Signal | Tool | Access for agents |
| --- | --- | --- |
| Errors (backend + frontend) | **Sentry free tier** — `sentry-go` in the backend, `@sentry/react` in the client (error boundaries, source maps; 5k errors + 5M spans/mo, 30-day retention) | Official hosted MCP server (`mcp.sentry.dev`), OAuth, un-gated on free; REST API fallback |
| Metrics + traces | **Grafana Cloud free tier** via **OpenTelemetry** — otel-go exports OTLP straight from the binary (no collector): `otelhttp` around the API, `otelpgx` on the Postgres pool, spans around brain dispatch and outbox delivery | `grafana/mcp-grafana` (PromQL, LogQL, TraceQL, alert rules, Sift) with a read-scoped service-account token; Prometheus/Loki HTTP APIs |
| Logs | **Structured slog JSON to stdout**, captured by Render — unchanged from today | `render logs` CLI / MCP; shipping to Grafana Loki via the `otelslog` bridge is a later option, not v1.5 (§13, D4) |
| Uptime | **The sentinel workflow** curls `/healthz` on a 15-minute cron (§7) — no extra vendor | The probe result and history are GitHub Actions runs — already agent-readable |
| Cron liveness | **healthchecks.io free** dead-man switches — nightly backup (§10) and nightly e2e ping it on success; silence fires the alert | Management REST API |
| Human paging | **ntfy.sh** topic — alerts push to the developer's phone; free, `curl`-able, priority levels | Sending an alert *is* a curl — agents can page the human |

Instrumentation is ~30 lines of otel-go setup in `cmd/kiln` plus env config
(`OTEL_EXPORTER_OTLP_ENDPOINT`/`_HEADERS`, `SENTRY_DSN` — backend env; the frontend
DSN is a build-time constant, not a secret). Locally everything degrades to no-ops
when the env vars are unset; optionally the `grafana/otel-lgtm` container can join
`docker-compose.yml` as a dev twin, but that is not required for v1.5.

**Retention trade-off accepted:** 14 days (Grafana) / 30 days (Sentry). A one-user
system's incidents are investigated within days or not at all.

## 7. Alerting & the agent incident loop

The escalation path, end to end:

```
signal (sentinel probe fail · healthchecks silence · Sentry alert · nightly e2e red)
  → ntfy push to the human                    (always, in parallel)
  → GitHub Actions launches Claude Code       (the investigating agent)
      reads: render logs/status/deploys · Grafana MCP · Sentry MCP · git log
      writes: ONE GitHub issue — hypothesis, evidence, recommended action,
              and the exact remediation command (e.g. the rollback one-liner)
  → human (or a later, trusted agent tier) executes the remediation
```

**`sentinel.yml`:** a 15-minute cron probes `/healthz`; on failure — or on a
`repository_dispatch` fired by any webhook-capable alert source (healthchecks.io,
Sentry) — it runs `anthropics/claude-code-action` with:

- a **read-only** credential set (§8): investigation cannot mutate production;
- `--allowedTools` pinned to read verbs (`render logs/status/deploys list`, `git log`,
  MCP queries, `gh issue create`) and `--max-turns` + a workflow timeout as cost bounds;
- the repo checked out, so `CLAUDE.md` and the skill library load — the
  `production-ops` skill (§8) is the runbook it follows;
- output contract: open one issue titled `Prod: <hypothesis>`; never deploy, restart,
  or change anything.

**The autonomy ladder** — widen write access only per proven failure class, never
wholesale (published benchmarks put fully autonomous resolution near 14%; supervised
investigation is what demonstrably works — §13, D5):

1. **v1.5 (this spec): read + recommend.** The agent diagnoses and hands the human a
   command.
2. **Later, earned:** allow specific verbs for specific diagnoses — restart on
   crash-loop, previous-image rollback when the failing deploy is the newest one —
   by widening the sentinel's allowlist in a reviewed commit.
3. **Never autonomous:** schema changes, data mutation, secret rotation, resource
   deletion.

## 8. Agent access model

Every credential that exists, its scope, and where it lives:

| Credential | Scope | Lives in | Used by |
| --- | --- | --- | --- |
| Render API key #1 — "deploy" | Kiln-only workspace | GitHub Actions secret | `ci.yml` deploy job |
| Render API key #2 — "ops" | same workspace; used only with read/restart/rollback verbs | GitHub Actions secret + developer's local `.env` | sentinel agent (read verbs only); interactive Claude Code sessions |
| Grafana service-account token | read-only (`logs:read`, `metrics:read`, `traces:read`) | GitHub secret + local `.env` | mcp-grafana |
| Sentry MCP | OAuth per session; org-scoped | — | interactive + sentinel agents |
| `ANTHROPIC_API_KEY` | — | GitHub secret + local `.env` | sentinel's Claude Code run |
| healthchecks.io ping URLs | write-only pings (not secrets in practice) | workflow env | backup + e2e crons |

Render can't mint per-verb keys (§3), so the deploy/ops split is discipline plus the
dedicated-workspace blast radius, not cryptographic enforcement — accepted for a
hobby-scale system (§13, D1). GitHub Actions secrets are write-only via API: an agent
can rotate a secret (`gh secret set`) but never read one back — a good default.

**Local guardrails:** `.claude/settings.json` gains a permissions allowlist — auto-allow
the read verbs (`render logs`, `render deploys list`, `render services`, `gh run list`),
prompt on mutating verbs (`render deploys create`, restart, rollback), deny
`render psql` writes and anything destructive. Mirrors the sentinel's allowlist so the
same policy governs both surfaces.

**The runbook is a skill.** A new `production-ops` skill in `.agents/skills` — the
`02` §4c pattern applied to operations: how to check prod health, read each signal,
the rollback one-liner, the restore procedure (§10), and the standing rules ("schema
never rolls back," "never touch prod data by hand"). Agents update it as they operate,
so every incident makes the next agent smarter. `AGENTS.md` gains a short "production
exists now" section pointing at it.

## 9. Secrets

- **Two stores, no new vendor** (§13, D6): GitHub Actions secrets (CI + sentinel) and
  Render env vars/env groups (runtime). Local dev keeps the gitignored `.env` +
  committed `.env.example` convention, now also listing `SENTRY_DSN` and the OTLP vars.
- **GitHub push protection + secret scanning: enabled.** Agents writing infra files
  make repo-leaked credentials the top risk; scanning is the machine-checkable
  guardrail, in the `02` §4 spirit. The `AGENTS.md` "never dump `.env`" rule now also
  covers `render env` output and CI logs.
- Rotation is manual and rare; at one developer, secret-sync platforms (Doppler,
  Infisical) solve problems we don't have.

## 10. Backups

- **Baseline:** Render managed Postgres — daily snapshots + point-in-time recovery.
- **Belt-and-suspenders:** `backup.yml`, a nightly GitHub Actions cron: `pg_dump` →
  write to disk → **verify non-zero size** → upload to Cloudflare R2 (free tier) →
  ping healthchecks.io. The dead-man switch alerts on backup *absence* — the failure
  mode cron-success alerting misses. Protects against the provider-account-loss case
  snapshots can't.
- **A backup that has never been restored is a hypothesis.** One restore drill
  (R2 dump → scratch Postgres → backend boots against it) when phase 4 lands; the
  procedure goes in the `production-ops` skill. Repeat only after major schema changes.

## 11. Rollout phases

Each phase is a normal gated change; each has a §12 verification before the next
starts.

1. **CI** — `ci.yml` gate job only (no deploy). Closes the gap between `02` §4's
   "CI runs the hard gate" and today's reality (no workflow exists). Plus push
   protection + secret scanning.
2. **Production** — `/healthz`; `render.yaml`; dedicated workspace; deploy job wired
   into `ci.yml`; SSE-through-rewrite verified (or the D2 fallback taken).
3. **Observability** — Sentry both sides; otel-go OTLP → Grafana Cloud; `.env.example`
   and Render env updated.
4. **Operations** — `sentinel.yml` + `backup.yml`; healthchecks.io + ntfy wired;
   `production-ops` skill written; restore drill done; nightly e2e schedule moved
   into CI.

## 12. Verification

Infrastructure claims get the same evidence standard as code (`02` §4):

- **CI:** a deliberately red commit on a branch fails the gate; a green `main` push
  deploys; a second push during a deploy queues rather than cancels.
- **Deploy:** clean-clone → push → live at the public URL, board + SSE stream working
  from a phone (the `01` product moment). Rollback drill: deploy, roll back via the
  documented one-liner, confirm the previous version serves.
- **Observability:** a forced backend panic appears in Sentry; a traced request is
  queryable in Grafana; `mcp-grafana` and Sentry MCP answer from an interactive
  Claude Code session using the scoped tokens (proving the §8 credentials work
  end-to-end).
- **Incident loop:** stop the Render service manually; within a probe cycle the
  sentinel fires, the agent files an issue naming the outage and the fix, ntfy pushes
  to the phone. That drill passing *is* the acceptance test for this spec's core
  promise.
- **Backups:** §10's restore drill.

## 13. Decision log

| # | Decision | Alternatives considered | Rationale |
| - | --- | --- | --- |
| D1 | **Render** as the hosting provider (~$13/mo: $7 service + $6 Postgres + free static site). | **Fly.io** — best-in-class scoped tokens and $2–6/mo compute, but managed Postgres starts at $38/mo, unmanaged postgres-flex is explicitly unsupported DIY, and its 2025–26 reliability record (incidents on 20 of 31 days in May 2026) is wrong for a stateful app operated by agents. **Railway** — $5–15/mo and pleasant, but topology isn't in repo files and rollback/backup-restore are dashboard actions — fails everything-as-code. **Hetzner + Kamal** — cheapest and fully repo-defined, but the agent credential is root SSH (worst blast radius) and we'd own DB backups, TLS, and patching. **Northflank** — strong API/IaC and near-$0, but a younger bet. | Only Render puts the entire stack in one repo file (`render.yaml`) *and* exposes every operational verb (deploy, logs, restart, rollback, one-off jobs, psql) through a GA non-interactive CLI plus a first-party MCP server, *and* has managed Postgres with PITR at hobby cost. Its coarse API-key scoping is the known cost, paid down via the dedicated workspace (§3, §8). |
| D2 | Frontend stays a separate deployable: Render static site + `/api/*` rewrite. | Serve `frontend/dist` from the Go binary (`embed.FS`); CORS + absolute API URLs. | Preserves `02` §2's two-deployable topology and same-origin `/api` unchanged; free CDN hosting. Explicit contingency: if SSE through the rewrite proxy misbehaves, fall back to embedding — one deployable, same-origin trivially true. CORS rejected: config surface + a changed client contract for zero gain. |
| D3 | Keep the existing embedded startup migrator; forward-only + expand/contract; rollback = previous-image redeploy. | goose / golang-migrate / Atlas; Render pre-deploy migration step; trusted down-migrations. | The `wiring.go` migrator already does the one thing needed at single-instance scale, and startup-coupled migration failure self-gates via the health check. A separate pre-deploy step earns its keep only with multiple instances/canaries — not v1.5. Down-migrations in prod are industry-distrusted; expand/contract is what actually makes rollback safe. |
| D4 | Observability = Sentry free + Grafana Cloud free (OTLP direct from the binary) + slog-to-stdout logs + sentinel probe + healthchecks.io + ntfy. | Axiom (500 GB/mo but weak metrics); Honeycomb (query API enterprise-gated); Better Stack (3-day retention); self-hosted LGTM (a second system to operate); Datadog-class (cost). | The only $0 combination where every signal is agent-reachable through an official, un-paywalled API or MCP server — the spec's first-class requirement. Logs stay on stdout/Render for v1.5: one fewer pipe, already CLI-queryable; Loki-via-`otelslog` is a later option if cross-signal correlation earns it. |
| D5 | Incident agents start read-only: investigate + file an issue with the exact fix command; autonomy widens per proven failure class only. | Auto-remediation from day one; no agent involvement (plain alerts). | Supervised investigation is the demonstrated-real slice of "AI SRE" (autonomous-resolution benchmarks sit near 14%); a wrong diagnosis that only produces an issue is free, a wrong rollback isn't. The ladder (§7) makes expanding autonomy a reviewed one-line diff, not an architecture change. |
| D6 | Secrets: GitHub Actions secrets + Render env only; push protection on. | Doppler / Infisical / 1Password automation. | Sync/rotation/audit platforms solve team problems; at one human the marginal safety is ~zero and it's another credential to leak. Scanning + write-only CI secrets are the guardrails that pay rent here. |
| D7 | e2e runs nightly + on demand, never as a deploy blocker. | Full `02` §4 gate including e2e on every push. | The e2e spends real provider money and inherits real-service flake (`02` §1 accepted this). A flaky wall teaches agents to distrust the wall — worse than a narrower, always-credible gate. Nightly red still alerts via §7. |

**Open questions (deliberately later):** shipping logs to Loki via `otelslog` (D4);
widening sentinel autonomy to restart/rollback (D5, ladder step 2); the
`grafana/otel-lgtm` local dev twin; frontend OTel tracing stitched to backend spans
(Sentry↔OTLP propagation); whether the nightly e2e should gate anything once
deterministic fakes (`02` §1) exist; push-notification infra interaction (`10`) once
that spec lands.
