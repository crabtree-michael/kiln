# Debug "Reset to fresh session" button — design

Date: 2026-07-04

## Purpose

Give the developer a one-click way, from the `/debug` view, to return the whole
system to a clean, fresh agent session: no board tickets, no chat/message
history, no live Amika sandboxes. This is the manual reset sequence (truncate
state tables + destroy `kiln-worker-*` sandboxes) turned into a button.

## Scope

- **Sandbox teardown scope:** only `kiln-worker-*` sandboxes (the adapter's own
  workers, via `ListWorkers`' existing name filter). Unrelated sandboxes on the
  Amika account are left alone.
- **Confirmation:** the button is guarded by a confirm dialog before it fires
  (the action is destructive).
- **Not gated:** the endpoint is mounted unconditionally — no `KILN_DEV_ENDPOINTS`
  gate — so the button works locally with no env setup.

## Backend

### Endpoint

`POST /api/dev/reset` on `api.Server`, mounted unconditionally in `Handler()`.
No request body; returns `204 No Content` on success. It is **not** part of the
wire contract (`/schema`) — like the other `/api/dev/*` routes — so there is no
type regeneration.

`api.Server` gains a `Resetter` port:

```go
type Resetter interface {
    Reset(ctx context.Context) error
}
```

satisfied by the composition-root coordinator below. The handler calls
`Reset(r.Context())`; on error it logs and returns `500`.

### Reset coordinator (composition root, `cmd/kiln`)

The reset crosses modules (DB truncation + agent-service teardown), so it is
orchestrated where the shared `*sql.DB`, `agentSvc`, and `hub` already live
(`wiring.go`). Order matters:

1. **Truncate state.** One statement:
   `TRUNCATE tickets, workers, outbox, messages, events, agent_turns,
   notifications RESTART IDENTITY CASCADE`. `schema_migrations` is left intact.
   Truncating first empties the board's "wanted" worker set, so the agent
   reconcile loop will not re-provision workers mid-reset.
2. **Tear down sandboxes + clear in-memory cache.** Call `agentSvc.Reset(ctx)`
   (new method, see below). A bare DB truncate is not enough on its own: the
   agent `Service` holds an in-memory `workers` map cache; without clearing it,
   stale worker references survive (this is why a manual reset previously
   required a backend restart).

The coordinator returns the first error but attempts teardown even if it logs a
per-sandbox failure (best-effort).

### `agent.Service.Reset(ctx)`

New method on `internal/agent.Service`:

- List live workers via `provider.ListWorkers` (already filtered to
  `kiln-worker-*`).
- `DestroyWorker` each; a `404` already counts as success in the adapter. A
  destroy failure is logged, not fatal — reset continues.
- Clear the in-memory `workers` map, under the same mutex that guards
  `getWorker`/`putWorker`/`lookupWorker`, so it is safe against the concurrent
  reconcile loop and turn execution.

## Frontend (`/debug` view only)

A **"Reset session"** button in `App.tsx`'s header (`app-header`, beside the
`Kiln` title). It lives only in the `/debug` route, so it never reaches the
primary client.

On click:

1. `window.confirm("Reset to a fresh session? This wipes the board, chat, and all sandboxes.")`.
2. On confirm, `POST /api/dev/reset`.
3. On a 2xx response, `window.location.reload()`.

The reload delivers the "whole new fresh session" result: it re-fetches the now
empty `GET /api/board` and reopens the SSE stream. No hub broadcast API is
needed. On a non-2xx response, surface a lightweight error (log + the button
returns to idle); no reload.

## Testing

- **Backend unit — coordinator:** with fakes, asserts truncate runs then
  `agentSvc.Reset` is called (order), and that a teardown error is surfaced but
  truncation still happened.
- **Backend unit — `agent.Service.Reset`:** with the existing fake provider,
  asserts every live worker is destroyed and the in-memory cache is emptied;
  a destroy error on one worker does not abort the others.
- **Backend unit — route:** `POST /api/dev/reset` calls the `Resetter` and maps
  success→204, error→500.
- **Frontend:** button renders in the debug view; confirm gates the call
  (decline → no fetch, no reload); on OK it POSTs and reloads
  (`window.confirm`, `fetch`, `window.location.reload` mocked).
- **Manual verify:** against the running stack — seed a ticket/chat, click the
  button, confirm the board and chat come back empty and sandboxes are gone.

## Out of scope

- Auth / rate-limiting on the endpoint (local dev tool).
- Auto-refreshing other connected clients (only the initiating client reloads).
- Resetting anything outside the listed tables (e.g. `internal/repo` state).
