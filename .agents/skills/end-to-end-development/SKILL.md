---
name: end-to-end-development
description: Use when doing any slice of development work in Kiln — before committing or merging any change. Covers the hard gate (lint, type-check, tests at three levels), working behind interfaces, the /schema wire-contract regen rule, and the e2e lanes. Spec 02 §3, §4.
---

# End-to-end development in Kiln (doc 02 §3, §4)

The area-agnostic working agreement every agent follows, regardless of module. Kiln is built
largely by coding agents, so the harness — not luck — is what catches mistakes.

- **The hard gate is a wall.** Linters + type-check/build + tests must be green before you
  commit or merge. Red means you cannot land. **Never weaken a check to make it pass.**
- **Three levels of tests.** Every module has **unit** tests and component-level
  **integration** tests; the whole system has **end-to-end** tests that exercise the real
  loop live.
- **Work behind interfaces.** Each backend module talks to its neighbours through an explicit
  contract; test a service against **fakes** (in-memory repo, scripted LLM), not real infra.
  Stay inside your area's boundary.
- **Update your area's skill as you work** (`AGENTS.md`): fold spec detail, gotchas, and
  how-to-run notes into the surface-area skill so the next agent inherits them. Write down the
  *why* and the non-obvious constraint — not a paraphrase of what the code already says.

## Where the specs actually live

**`docs/specs/02-initial-technical-architecture.md` stops at §4.** It is a template whose
surface-area sections (§5 board, §6 brain, §7 runtime, §8 agent runtime, §9 voice,
§10 notifications, §11 client, §14 deferred choices) were **never written** — they became
separate numbered docs instead. Any `02 §5`–`§14` citation you meet in an older doc or comment
is dangling; follow this table instead. Only `02` §1–§4 (scope, topology, stack, DevOps) are
real, and they are still the framing every skill sits under.

| Area | Spec |
| --- | --- |
| Board mechanics | `03-board-mechanics.md` |
| Runtime / queue / API | `04-runtime-and-api.md` |
| Agent runtime | `05-agent-runtime.md` |
| Orchestrator brain | `06-orchestrator-brain.md` |
| Text client | `07-v1-text-client.md` |
| Primary screen / interaction | `08-user-interaction.md` |
| Voice | `09-voice-pipeline.md` |
| Infrastructure & production | `10-infrastructure.md` |
| Multi-user | `11-multi-user.md` · Multi-project `12-multi-project.md` |
| Desktop + kanban | `13-desktop-experience.md` |

**Notifications have no spec doc** — the decisions live in the `notifications` skill and the
push design docs under `docs/superpowers/specs/`. Note `10-infrastructure.md` is *infra*, not
notifications, despite "§10" meaning push in older prose.

## The wire contract lives in `/schema` (02 §3)

`schema/openapi.yaml` is the single source of truth for the client↔server boundary — the
contract two parallel agents agree on before writing code. It generates **both** sides:
`backend/internal/wire/generated.go` (oapi-codegen) and `frontend/src/schema/generated.ts`
(openapi-typescript). See `schema/README.md`.

- **Never hand-edit generated types.** Change `openapi.yaml`, run **`make schema`**, and commit
  schema + generated Go + generated TS **together in one commit** (§4). Changing one side's
  types by hand to match the other is how the two drift.
- Keep `openapi.yaml` a valid **OpenAPI 3.0.x** document — it is pinned to 3.0.x because
  oapi-codegen does not fully support 3.1, and `make schema` fails loud if it isn't.
- `make schema-verify` regenerates and fails on any diff. It is the first step of `make check`,
  so stale generated types fail the gate.

## How to work here

1. Read your area's surface-area skill (e.g. `board-mechanism`, `web-client`).
2. If the change touches the client↔server boundary, edit `/schema` and regenerate both sides.
3. Develop test-first against fakes; keep inside your module boundary.
4. Run **`make check`** — the wall. Green before you commit. (CI runs the same `make check` on
   every push and PR — `.github/workflows/check.yml`.)
5. Isolate parallel work via a branch/worktree off the single monorepo.
6. Update your area's skill with anything you learned.

Install the hooks once with `make hooks` (pre-commit = lint + typecheck, pre-push = full gate).

## Running the tests

**`make check` = `schema-verify → lint → typecheck → test`.** It needs no stack and no
provider keys, and is the only thing that gates a commit. `make test` is its test third:

- **Backend** — `go test ./...`, then `go test -race -tags=integration -p 1 ./...`. The `-p 1`
  is load-bearing: the integration packages share one mutable `kiln_test` database and reset it
  with TRUNCATE, so running them concurrently wipes each other's rows mid-test. `-race` because
  the multi-instance concurrency tests (leader election, two Services over one store) live here.
- **Frontend** — `pnpm test` (Vitest, jsdom).
- **Layout** — `make test-layout` (Playwright over the real client, every `/api` call stubbed,
  asserting **computed geometry**). It is in the gate because it needs no stack and no keys
  (~1 min). **jsdom performs no layout**, so this is the only level that can see a layout bug
  at all — a whole class of UI bug shipped repeatedly with the unit gate green. First run:
  `cd tests && pnpm install && pnpm run install-browser`.

**End-to-end is separate and deliberate — it is not in the commit gate.** Both lanes live in
`/tests` (Playwright) and drive the **real web client** against a **running stack**. Full recipe,
per-spec notes, and cleanup rules: **`/tests/README.md`** — read it rather than reconstructing
the commands.

- **Keyless lane (`@keyless`) — prefer this.** `make up-keyless` brings the stack up with every
  paid boundary mocked (mock agent provider, scripted brain, mock STT/verify/GitHub, test VAPID
  pair) **and seeds the `e2e-user` project**, so there is no onboarding step and no bill. Then
  `make e2e-keyless`, `make down-keyless`. `keyless-onboarding.spec.ts` is the one spec that
  drives the guided setup flow end to end (it needs `KILN_GITHUB_MODE=mock`); don't couple new
  specs to that flow — seed a project over `PUT /api/project` instead.
- **Real-service lane.** Hits the real LLM, Amika and AssemblyAI, so it **bills money** and
  needs keys. Bring the stack up on the cheap model (`KILN_BRAIN_MODEL=claude-haiku-4-5-20251001
  make up`), **onboard a project for the test user once per fresh DB** (a fresh stack has none,
  and the specs fail at `expect(board).toBeVisible()` — recipe in `/tests/README.md`), then
  `make e2e`. `make down` deletes the DB volume, so re-seed after a teardown.
- **Sandbox cleanup is not optional.** `auto_delete` is off by design (05 D6), so any spec that
  reaches Developing leaks sandboxes. `global-teardown.ts` deletes this stack's own pool, scoped
  by `KILN_WORKER_PREFIX` (default `kiln-dev-worker-`) so it can never touch another
  environment's sandboxes on the shared account.

**E2e tests live in `/tests`, never in Go.** A Go `_test.go` — even a build-tagged, env-gated
one — can only reach its own module's fakes/ports in-process; it cannot exercise the live loop,
and a real-service Go test also drags network/credential side effects (e.g. host git credential
helpers touching the OS keychain) into the unit gate. If you're tempted to write a
`//go:build e2e` Go test, that's the signal to write a `tests/*.spec.ts` instead.

## Common footguns

- Weakening or skipping a check (disabling a lint rule, `-skip`, `xit`) to get to green.
- Hand-editing generated types instead of the schema.
- Reaching across a module boundary instead of through its interface.
- Writing an end-to-end test as a Go `_test.go` instead of a `tests/*.spec.ts`.
- Reaching for the real-service lane when the keyless one would do — it bills money and needs
  an onboarding step the keyless stack seeds for you.
