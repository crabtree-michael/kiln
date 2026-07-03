---
name: notification-transport
description: Work in the Kiln notification transport — push that reaches the user when the app is backgrounded/closed, plus the deep link that opens the app to an updated board with voice attached. Use when editing push integration, notification triggers, deep links, or token registration.
---

# Notification transport (push)

**Spec:** `docs/specs/02-initial-technical-architecture.md` §10, realizing
`docs/specs/01-initial.md` §7. Push is **real in v1** (02 §1).

## Responsibility

Reach the user when the app is backgrounded or closed and the orchestrator needs
them (e.g. a ticket moving to Blocked).

## Interface

- A `send-notification` capability the brain/runtime (§6/§7) invoke.
- A **deep link** that opens the app to an already-updated board with the voice
  channel attached (§11).

## Where it lives

A push adapter behind a port, injected at the composition root; the runtime calls
the port. Registration + tap-handling live in the web client (§11).

## What this area still has to decide (02 §10)

- Push transport (web push / FCM / APNs) and its **mobile-web constraints** —
  this interacts with the PWA-vs-wrapped-native call in web-client §11.
- Which events fire a notification (Blocked is the canonical one).
- Deep-link / tap-to-open behavior.
- Registration and **token lifecycle**.

## Gotchas

- Mobile-web push is the constraint that may force the PWA-vs-native decision in
  §11 — decide the two together, not in isolation.
- The deep link must land on an **already-updated** board (state is server-side,
  `01` §4); the tap opens a view, it does not carry state.

## Keep this skill current

Record the transport choice, which events notify, and token-lifecycle details.
