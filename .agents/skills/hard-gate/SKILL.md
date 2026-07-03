---
name: hard-gate
description: How to develop in Kiln end-to-end — the hard gate (lint, type-check, tests), where to run it, the module boundaries and ports pattern, and how to land a change. Use at the start of ANY Kiln backend or frontend task, before committing, or when a check is failing.
---

# Developing in Kiln — the hard gate

**Spec:** `docs/specs/02-initial-technical-architecture.md` §4 + `AGENTS.md`.
This is the general how-to-work skill; every agent reads it first.

## The one rule

**The green checkmark is a wall, not a suggestion.** Lint + type-check + tests
must pass before you commit or merge. **Never weaken a check to make it pass** —
fix the code. If a rule seems wrong, raise it in the decision log (02 §16); do not
silently disable it.

## Run the gate

```bash
make check          # full gate: lint + type-check/build + tests (both surfaces)
make lint           # golangci-lint + gofmt + eslint + prettier
make typecheck      # go build ./...  +  tsc --noEmit
make test           # go test (unit + integration) + vitest
```

Per surface: `make check-backend`-style targets don't exist; scope with the tools
directly, e.g. `cd backend && go test ./internal/board/...`.

Install the hooks once so the gate runs automatically:

```bash
make hooks          # pre-commit = lint+typecheck ; pre-push = full check
```

## How code is structured (02 §2)

- **Stay inside your module's boundary.** Backend modules
  (`api·runtime·brain·board·amika`) talk to neighbors only through explicit
  interfaces. Frontend holds no authoritative state.
- **Depend on ports, not concretes.** A service names its dependencies as
  interfaces (a repo, an LLM, Amika) and is tested against **fakes** — no real
  Postgres/LLM/Amika in a unit test. Infra is constructed **only** at the
  composition root (`backend/cmd/kiln`) and injected upward.
- **Every module keeps at least one real passing test** so its gate is meaningful.

## Landing a change

1. Work behind your module's interface; add/adjust ports as needed.
2. If you touched the client↔server boundary, change `/schema` and run
   `make schema` — never hand-edit generated types (see `wire-contract`).
3. `make check` is green.
4. **Update your area's skill** with anything you learned (AGENTS.md, 02 §4c).

## Escape hatches are banned (02 §4b)

Backend: no ignoring `err`, aggressive `golangci-lint`. Frontend: no `any`, `as`,
`@ts-ignore`, non-null `!`, or unused symbols — the linter fails on them.
