---
name: web-client
description: Work in the Kiln web client — the thin, disposable, mobile-first PWA that renders the board over a live connection, captures mic audio, plays voice, and handles push. Use when editing /frontend, React components, the live-connection client, PWA/service-worker, or reconnection/resync.
---

# Web client (/frontend)

**Spec:** `docs/specs/02-initial-technical-architecture.md` §11, realizing
`docs/specs/01-initial.md` §4.

## Responsibility

A deliberately **thin, disposable, mobile-first** surface: renders the board over
a live connection, captures mic audio, plays Kiln's voice, receives
notifications. **Holds no authoritative state** — all state is server-side.

## Stack (harness defaults, 02 §11)

Vite + React + TypeScript, mobile-first **PWA** (`vite-plugin-pwa`). Vitest +
Testing Library. Types are **generated** from `/schema` (see `wire-contract`).

## The gate you must keep green (02 §4b)

`pnpm check` = lint + typecheck + test. The lint config **bans the escape
hatches** — `any`, `as`, `@ts-ignore`, non-null `!`, unused symbols are hard
errors. Do not reach for them; narrow with type guards and fix the types.

```bash
cd frontend && pnpm check      # lint + typecheck + test
cd frontend && pnpm dev        # local dev server (proxies /api to backend)
```

## What this area still has to decide (02 §11)

- Live-connection transport — **WS vs SSE, shared with runtime §7**.
- Board rendering and how live updates are applied.
- PWA vs wrapped-native for mic + push on mobile (interacts with notifications
  §10).
- Reconnection / resync after the connection drops (state is server-side, so
  resync = re-fetch, not replay).

## Gotchas

- Never introduce authoritative client state; if you feel the need, the data
  belongs on the server (§5/§7).
- Never hand-edit `src/schema/generated.ts` — regenerate from `/schema`.

## Keep this skill current

Record the transport decision, resync strategy, and PWA/native call here.
