---
name: end-to-end-development
description: Use when doing any slice of development work in Kiln — before committing or merging any change. Covers the hard gate (lint, type-check, tests at three levels), working behind interfaces, the wire-schema regen rule, and parallel-agent isolation. Spec 02 §4.
---

# End-to-end development in Kiln (doc 02 §4)

## Functional Requirements

The area-agnostic working agreement every agent follows, regardless of module. Kiln is built
largely by coding agents, so the harness — not luck — is what catches mistakes.

- **The hard gate is a wall.** Linters + type-check/build + tests must be green before you
  commit or merge. Red means you cannot land. **Never weaken a check to make it pass.**
- **Three levels of tests.** Every module has **unit** tests and component-level
  **integration** tests; the whole system has an **end-to-end** test that exercises the real
  loop live. Test-framework choices are deferred (§14).
- **E2e tests live in `/tests`, never in Go.** The `/tests` Playwright suite is the *only*
  home for end-to-end tests — they drive the running stack over HTTP and the real browser, so
  the brain hits the real LLM and real providers. A Go `_test.go` (even a build-tagged, env-
  gated one) can only reach its own module's fakes/ports in-process; it cannot exercise the
  live loop, and a real-service Go test also drags network/credential side effects (e.g. host
  git credential helpers touching the OS keychain) into the unit gate. If you're tempted to
  write a `//go:build e2e` Go test, that's the signal to write a `tests/*.spec.ts` instead.
- **Work behind interfaces.** Each backend module talks to its neighbors through an explicit
  contract; test a service against **fakes** (in-memory repo, scripted LLM), not real infra.
  Stay inside your area's boundary.
- **The wire contract lives in `/schema`.** Never hand-edit generated types — change the
  schema and regenerate both Go and TS (see `wire-schema`).
- **Update your area's skill as you work** (`AGENTS.md`): fold spec detail, gotchas, and
  how-to-run notes into the surface-area skill so the next agent inherits them.

## How to work here

1. Read your area's surface-area skill (e.g. `board-mechanism`, `web-client`).
2. If the change touches the client↔server boundary, edit `/schema` and regenerate both sides.
3. Develop test-first against fakes; keep inside your module boundary.
4. Run the full hard gate locally: **lint → type-check/build → unit → integration → e2e**.
   Green before you commit. (CI runs the same gate on every push and PR — §4.)
5. Isolate parallel work via a branch/worktree off the single monorepo.
6. Update your area's skill with anything you learned.

## Running the tests

The three levels run in two places. **Unit + component-integration are the commit gate** and
run offline against fakes; **e2e is separate** and needs a live stack.

- **The gate (offline, fakes):** `make check` — the wall (`lint → typecheck → test`). `make test`
  alone runs both surfaces' unit + integration:
  - Backend: `cd backend && go test ./...` then `go test -tags=integration ./...`.
  - Frontend: `cd frontend && pnpm test` (Vitest).
  Green before you commit. Never `-skip`/`xit` a check to get there.

- **End-to-end (live stack, real services):** the suite lives in **`/tests`** (Playwright) and
  drives the **real web client** against a running stack — no fakes, so the brain hits the real
  LLM (§4a, §1). Run it deliberately, not in the commit gate:
  1. Bring the stack up on the cheap model with a real key:
     `KILN_BRAIN_MODEL=claude-haiku-4-5-20251001 make up` (real runs bill money — use Haiku).
  2. First time: `cd tests && pnpm install && pnpm run install-browser`.
  3. `make e2e` (i.e. `cd tests && pnpm test`). It targets the docker-compose frontend
     (`http://localhost:5173`) by default; override with `KILN_E2E_BASE_URL`.
  Any e2e that reaches Developing must destroy the Amika sandboxes it creates (`auto_delete` is
  off — 05 D6). `say → ticket in Backlog` stops before the pull, so no cleanup;
  `ready-kicks-off-amika-run` (API-driven: `POST /api/message` → brain marks ready → pull →
  real Amika turn, asserting both the ticket reaches `working` and — reaching past the 05 §1
  abstraction on purpose, to verify the default provider — that the bound sandbox gains a new
  Amika session, since v0beta1 has no list-jobs endpoint) does reach Developing and is cleaned
  up automatically by Playwright's `global-teardown.ts`, which deletes the stack's own
  worker pool — scoped by `KILN_WORKER_PREFIX`, default `kiln-dev-worker-`, so it never
  touches another environment's (e.g. prod's) sandboxes on the shared account
  (best-effort while the stack is up — the reconciler recreates idle slots, so run `make down`
  after for a clean slate). See `/tests/README.md` for the full recipe.

## Common footguns

- Weakening or skipping a check (disabling a lint rule, `-skip`, `xit`) to get to green.
- Hand-editing generated types instead of the schema.
- Reaching across a module boundary instead of through its interface.
- Writing an end-to-end test as a Go `_test.go` (build-tagged or otherwise) instead of a
  `tests/*.spec.ts`. E2e drives the *running stack*, not in-process fakes — it belongs in
  `/tests`.

## Potential gotchas

- **v1 e2e hits real services** (LLM, Amika, STT/TTS) — there are no deterministic local
  fakes at the e2e level yet (§1); that is a later optimization.

_(Accumulate more as the harness fills in.)_
