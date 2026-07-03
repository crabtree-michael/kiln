# Kiln — Agent Working Agreement

This repo is built largely by coding agents. See `docs/specs/01-initial.md` (product) and
`docs/specs/02-initial-technical-architecture.md` (how it's built).

## Ground rules

- **The hard gate is a wall.** Linters + type-check + tests must be green before you commit or
  merge. Red means you cannot land. Do not weaken a check to make it pass.
- **Work behind interfaces.** Each backend module (`backend/internal/{api,runtime,brain,board,amika}`)
  talks to its neighbors through an explicit contract. Stay inside your area's boundary.
- **The wire contract lives in `/schema`.** Never hand-edit generated types — change the schema and
  regenerate both Go and TS.
- **Update your area's skill as you work.** Each surface area has a skill under `.agents/skills`.
  When you learn something about your area — spec detail, a gotcha, how to run it — write it into
  that skill so the next agent inherits it.

## Layout

```
/backend    Go orchestrator (api · runtime · brain · board · amika)
/frontend   TS/React client
/schema     language-neutral wire contract (generates Go + TS types)
.agents/skills   canonical skills, symlinked to .claude/skills and .codex/skills
```
