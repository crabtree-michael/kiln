# Kiln skill library

Canonical skills (02 §4c/§4d), symlinked into `.claude/skills` and `.codex/skills`
so the same skills feed whatever agent tool is driving. **Authored here only.**

**As you work an area, update its skill** (AGENTS.md) — each is living
documentation that accumulates the area's spec and hard-won detail over time.

## General (read first, any task)

| Skill | Use for |
| ----- | ------- |
| [hard-gate](hard-gate/) | The gate + module/ports pattern + how to land a change |
| [local-environment](local-environment/) | Running the stack locally (Compose, ports, secrets) |
| [wire-contract](wire-contract/) | Changing the client↔server boundary (`/schema`) |

## Per surface area (02 §§5–11)

| Skill | Module / area | Spec |
| ----- | ------------- | ---- |
| [board-mechanism](board-mechanism/) | `backend/internal/board` | §5 |
| [orchestrator-brain](orchestrator-brain/) | `backend/internal/brain` | §6 |
| [runtime-and-api](runtime-and-api/) | `backend/internal/{runtime,api}` | §7 |
| [amika-integration](amika-integration/) | `backend/internal/amika` | §8 |
| [voice-pipeline](voice-pipeline/) | STT/TTS I/O layer | §9 |
| [notification-transport](notification-transport/) | push + deep links | §10 |
| [web-client](web-client/) | `/frontend` | §11 |
