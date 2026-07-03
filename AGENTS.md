# Kiln — Agent Working Agreement

This repo is built largely by coding agents. See `docs/specs/01-initial.md` (product) and
`docs/specs/02-initial-technical-architecture.md` (how it's built).

## Start here

1. Read the `end-to-end-development` skill (the gate + how to land a change), then your area's skill.
2. Install the gate hooks once: `make hooks` (pre-commit = lint+typecheck, pre-push = full gate).
3. Run the gate before you commit: `make check` (lint → type-check/build → unit + integration).
4. Bring the stack up locally: `make up` (see the `local-environment` skill).

## Ground rules

- **The hard gate is a wall.** Linters + type-check + tests must be green before you commit or
  merge. Red means you cannot land. Do not weaken a check to make it pass.
- **Work behind interfaces.** Each backend module (`backend/internal/{api,runtime,brain,board,agent}`)
  talks to its neighbors through an explicit contract. Stay inside your area's boundary.
- **The wire contract lives in `/schema`.** Never hand-edit generated types — change the schema and
  regenerate both Go and TS.
- **Update your area's skill as you work.** Each surface area has a skill under `.agents/skills`.
  When you learn something about your area — spec detail, a gotcha, how to run it — write it into
  that skill so the next agent inherits it.

## Skills

Canonical skills live in `.agents/skills` (symlinked into `.claude/skills` and `.codex/skills`).
Read your area's skill before working; keep it current as you go. Every agent starts from the two
general skills.

| Skill | Area | Spec |
| ----- | ---- | ---- |
| `end-to-end-development` | General — the hard gate, interfaces, schema regen, isolation | §4 |
| `local-environment` | General — bring the system up via `docker compose up` | §1, §4 |
| `board-mechanism` | `backend/internal/board` | §5 |
| `orchestrator-brain` | `backend/internal/brain` | §6 |
| `runtime-and-api` | `backend/internal/runtime` · `internal/api` | §7 |
| `amika-integration` | `backend/internal/agent` — neutral agent-runtime layer; Amika adapter in `./amika` | §8, 05 |
| `voice-pipeline` | `/frontend` mic/playback · runtime bridge | §9 |
| `notifications` | `/frontend` registration/tap · runtime send path | §10 |
| `web-client` | `/frontend` | §11 |
| `wire-schema` | `/schema` | §3, §4 |