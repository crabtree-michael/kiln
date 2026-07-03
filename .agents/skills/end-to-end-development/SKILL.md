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

## Common footguns

- Weakening or skipping a check (disabling a lint rule, `-skip`, `xit`) to get to green.
- Hand-editing generated types instead of the schema.
- Reaching across a module boundary instead of through its interface.

## Potential gotchas

- **v1 e2e hits real services** (LLM, Amika, STT/TTS) — there are no deterministic local
  fakes at the e2e level yet (§1); that is a later optimization.

_(Accumulate more as the harness fills in.)_
