# Root cause — "I launched the sandbox for a ticket, but it didn't save on the Amika side"

**Date:** 2026-08-09 · **Service:** `srv-d953nmcvikkc73d8aq60` · **Project:** Pac-Man
(`04957194-c4de-4e8d-8032-918dbc875444`, `crabtree-michael/pacman`) ·
**Code investigated:** `83d5181` (the commit prod was running during the incident)

Investigation only — no product code changed here. The user's sandbox **was** kept alive exactly as
designed; what never happened was a *snapshot*, because the only UI that could create one had been
removed a week earlier.

> **Since fixed.** `a276c94` *"fix(agent): saving a sandbox captures a snapshot instead of just
> sparing it"* (merged `8d7804d`, live in prod **14:58:47**) makes the toggle actually capture, and
> landed while this investigation was running. §1 describes the cause of *this* incident; §4 records
> what it supersedes and what is still open. One item is **new and live**: the hand-captured image
> now serving Pac-Man was taken in the mode that commit calls unsafe.

## 1. Root cause: two features, one verb, one surviving UI

`4dc66db` *"feat(board): make saving a sandbox a per-ticket option"* (2026-08-02) deleted the
project-settings form that captured a running dev box as a named Amika snapshot, and replaced it
with a per-ticket toggle. The two did entirely different things:

| | Old (removed) | New at the time of the incident |
|---|---|---|
| Label | "Save to sandbox" | **"Save sandbox when done"** |
| Mechanism | `POST /api/projects/{id}/snapshots` → Amika `POST /sandbox-snapshots` | suppressed the `agent.release` emission |
| Result | durable named image in the Amika catalog | the live sandbox wasn't recycled |
| Survives a recycle | yes | **no** |

That commit left the capture endpoint mounted on purpose — *"nothing about moving the affordance
requires deleting them"* — but nothing called it:

- `backend/internal/api/routes.go` — `POST /snapshots` → `handleSaveSnapshot`, **zero callers**
  (still true: `a276c94` captures through the outbox, not this route)
- `frontend/src/transport/transport.ts` — `fetchSnapshots` is GET-only; no POST to `/snapshots`
  exists anywhere in the frontend
- `frontend/src/dashboard/use-sandbox-catalog.ts` — records the removal in its own comment
- `backend/internal/board/service.go` — the mechanism: everything except `agent.release`

So the user did the only thing the UI offered, it worked, and nothing was sent to Amika.
**Reproducible on every project and ticket**, and it logged nothing — the capture path was simply
unreachable from the client, so there was no failure to record.

## 2. What actually happened

Ticket `0a373248-a20d-4a03-958b-a2b5c92d9115`, "Pac-Man Mobile Web: Sandbox & Testing Environment
Setup" (body: *"the user intends to save it as a permanent snapshot"*). All times UTC.

| Time | Event |
|---|---|
| 12:44:51 | `set_keep_sandbox` — user toggles it on (no `turn_id`: direct API write) |
| 13:21:39 | pulled → working on slot `18cb9e10`; sandbox created 13:21:44 |
| 13:43:02 | turn completed — env verified, `npm run check` green |
| 13:46:23 | `accept_to_done`, `agent.release` **suppressed as designed** |

`keep_sandbox` worked end-to-end; the box was still `running` afterwards. But `amika snapshot list`
held nothing for pacman — and the agent had already told the user the truth at 13:46:26: *"to freeze
this as your permanent dev snapshot, you'll need to snapshot the machine image itself."*

## 3. Resolution (out-of-band, via the Amika CLI)

1. **Snapshot captured** 14:13:11 — `pacman-20260809141304`
   (`sbsnap_fc0d0e97-32b6-444e-99dd-9c6055e1c6f7`), `--mode full`, from base
   `amika-daytona-vm-m-c3b6f9c`.
2. **Project's sandbox snapshot** pointed at it by the user in Project Settings.

Verified by runtime behaviour rather than by reading the row: all three Pac-Man slots were recycled
at **14:54:26–14:54:32** and every one came up on base `pacman-20260809141304`, `setup: ok`. The
handle reaches sandbox creation via `Project.AmikaSnapshot` at provider build
(`backend/cmd/kiln/registry.go`), so three fresh boxes booting from it proves both the stored value
and that the runtime honours it. This happened **before** the 14:58:47 deploy, so it is attributable
to the manual setting change under `83d5181`, not to the new capture path.

**The near-miss.** That 14:54 recycle destroyed the original box holding the verified environment.
The capture ran **41 minutes before it**. Without the manual capture the workspace would now be
permanently gone — the concrete cost of the gap in §1: a "saved" sandbox was still one recycle away
from nothing.

### Verification limits worth recording

Neither read path was available from a worker sandbox, which is why §3 leans on behaviour:

- **DB** — `kiln-db`'s IP allowlist holds exactly one entry (the owner's own IP). Reaching it would
  have meant opening production Postgres to another network to read one column; not done.
- **API** — `PUT/GET /api/projects/{id}` sit behind `withSession`
  (`backend/internal/api/session.go`), a browser cookie with no token or service-account path.
- **Logs** — a *successful* project update emits nothing (`identity_handlers.go` logs errors only)
  and there is no access-log middleware.

## 4. Follow-ups

**1. Live: re-capture Pac-Man's base image in `scrub_and_delete`.** `pacman-20260809141304` was taken
with `--mode full`, which preserves the source box but **retains the injected secrets** — and it is
now the base image every Pac-Man worker starts from. `a276c94` reaches the same conclusion
independently and treats it as non-negotiable: *"scrub_and_delete is the only safe mode: a Kiln
worker holds the owner's git credential and the project's secrets, and this image is about to become
the base every future worker starts from."* The hand-captured image predates that rule and does not
follow it. Re-capture (or let the now-fixed toggle capture a fresh one) and delete this snapshot.

**2. Superseded — capture and naming.** `a276c94` swaps the suppression for a capture: the single
seam both exits from Developing route through emits `agent.snapshot` in place of `agent.release`,
the runtime routes it to `AgentRuntime.Snapshot`, and the composition root names the image
`<project>-<timestamp>` and repoints the project at it. So "Save sandbox when done" now means what
it says, and the rename this investigation recommended is moot — the behaviour moved to match the
label instead. Settings gained a line saying the toggle adds a snapshot and selects it.

**3. Still open — the caller-less HTTP endpoint.** `POST /api/projects/{id}/snapshots` remains
mounted with no caller; the fix captures through the outbox instead. Worth either wiring or
retiring, and worth noting that Kiln's endpoint is session-gated either way, so capture stays
unreachable to an agent or script.

**4. Still open — billing alert for Anthropic credit exhaustion.** Unrelated but found here: from
**14:33:28 to ~14:50** the brain hard-failed every event with `400 invalid_request_error: "Your
credit balance is too low"` — 105 error lines, events 2771–2782, in a retry loop. It recovered on
its own by 14:53. It stalls every project silently.
