// Package agent is the provider-neutral agent-runtime layer: the rest of Kiln
// sees workers (opaque capacity slots), Send (deliver a message to a worker),
// and turn output arriving as agent.turn_completed events — never sandboxes,
// sessions, or jobs. Amika is one provider adapter behind this contract.
//
// Spec: docs/specs/05-agent-runtime.md (realizing 02 §8 against Amika API
// v0beta1), on top of 01 §4's agent-platform integration.
//
// Abstraction rule (05 §1): no provider concept may leak out of this module.
// Swapping or adding an agent platform touches only the Provider adapter and
// configuration.
//
// Layering (see 02 §2). Outer layers depend inward, never the reverse:
//
//	interfaces  — the AgentRuntime contract the runtime's outbox worker calls
//	              (Send / Release, record-and-return), and the
//	              agent.turn_completed events emitted via the runtime's
//	              EnqueueEvent port.
//	services    — the per-operation turn state machine, the pool reconciler
//	              (adopt first, create only what's missing), and the poller;
//	              written once against the Provider port.
//	infra       — the agent_turns store (idempotency + recovery), the Amika
//	              v0beta1 HTTP adapter in ./amika, and the mock provider used
//	              in dev and e2e.
package agent
