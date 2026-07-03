---
name: notifications
description: Use when working on notification transport — reaching the user when the app is backgrounded or closed and the orchestrator needs them (e.g. a ticket moving to Blocked). Spans the frontend registration/tap-handling and the runtime send path. Spec 02 §10.
---

# Notification transport (doc 02 §10)

## Functional Requirements

> **Deferred in v1** (07 D1): `notify.send` outbox entries execute as a structured log
> line (07 §6); the Blocked zone is the in-app stand-in surface. This spec later swaps in
> a real push executor — the outbox contract (03 §7.1) is unchanged.

**Responsibility.** Reach the user when the app is backgrounded or closed and the
orchestrator needs them (`01` §7) — e.g. a ticket moving to Blocked.

**Interface.** A send-notification capability the brain / runtime invoke; a deep link that
opens the app to an already-updated board with the voice channel attached.

**Dependencies.** A push provider; the runtime (§7); the client (§11) for registration and
tap-handling.

**Open decisions — TBD → §10.**
- [ ] Push transport (web push / FCM / APNs) and its mobile-web constraints.
- [ ] Which events fire a notification.
- [ ] Deep-link / tap-to-open behavior.
- [ ] Registration and token lifecycle.

## How to work here

_(Accumulate: how to test the send path against a fake provider, where registration /
tap-handling lives in the frontend vs. the runtime send path.)_

## Common footguns

_(Accumulate: mistakes agents predictably make here.)_

## Potential gotchas

_(Accumulate: non-obvious traps and edge cases.)_
