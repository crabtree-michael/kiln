// Package runtime is the durable, deploy-resumable service shell: it drains
// the two durable queues (02 §2) — the event queue that wakes the brain, one
// LLM pass per entry, and the outbox of mechanical side effects — and owns
// delivery: retries, backoff, dead-lettering. Spec:
// docs/specs/04-runtime-and-api.md (realizing 02 §7 and 01 §8's
// deploy-resumable requirement).
//
// The board owns what goes *into* the outbox (03 §7); this module owns the
// delivery-state columns and everything that touches them (04 §2). Recovery
// needs no special code path: restart, poll, re-run whatever is pending
// (04 §5).
package runtime
