// Package steward is the mechanical stall watchdog over Working tickets: a
// fully deterministic periodic sweep (no brain/orchestrator judgment) that
// notices when a Working ticket's bound agent has gone idle/stopped with
// nothing in flight and nudges it, escalating to Blocked if it stalls again or
// errors after a nudge.
//
// It exists because a hung agent was previously only noticed when the user
// happened to ask. The sweep runs on its own clock-driven loop (mirroring the
// agent module's poller, not the runtime's event queue) and reaches the board,
// the agent runtime, and the feed only through the narrow ports declared in
// service.go — it never imports a sibling module's concrete type. The
// composition root supplies the adapters.
//
// The rules are intentionally conservative (see service.go): only idle/stopped
// (and, post-nudge, errored) agents are ever touched; an agent still actively
// `building` is left alone, because there is no safe way to tell a slow-but-fine
// turn from a hang. One poke per Working episode; a second sustained stall or a
// post-poke error surfaces the ticket as Blocked rather than leaving it stuck.
package steward
