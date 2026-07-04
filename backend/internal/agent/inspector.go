package agent

import "time"

// TurnOutput is the provider-neutral result of ReadLatestOutput (05 §2): the
// most recent completed assistant output for a worker's current conversation.
// Empty Output means "no completed turn yet" — never an error. No provider
// handle (session/sandbox id) is carried.
type TurnOutput struct {
	Output string
	At     time.Time
}

// AgentStatus is the neutral busy/free state the brain sees (05 §2). v1 is
// working|idle, derived from agent_turns; provider liveness beyond this is not
// surfaced (a pooled worker is up unless auto-stopped).
//
//nolint:revive // name fixed by 05 §2's list_agents/get_agent_updates contract, not a stutter accident
type AgentStatus string

const (
	AgentWorking AgentStatus = "working" // a send turn is in flight
	AgentIdle    AgentStatus = "idle"    // no turn running (done/released/none)
)

// AgentInfo is one running worker as list_agents reports it (05 §2). WorkerID is
// the board slot uuid (03 §2.3) — never a sandbox id. TicketID is the worker's
// most-recent send binding, "" if it never ran a send.
//
//nolint:revive // name fixed by 05 §2's list_agents/get_agent_updates contract, not a stutter accident
type AgentInfo struct {
	WorkerID  string
	TicketID  string
	Status    AgentStatus
	UpdatedAt time.Time
}

// AgentUpdate is get_agent_updates' answer for one worker (05 §2): its status
// plus the latest completed assistant output. IsError is best-effort from a
// terminally-failed turn row (the transcript read carries no error flag).
//
//nolint:revive // name fixed by 05 §2's list_agents/get_agent_updates contract, not a stutter accident
type AgentUpdate struct {
	WorkerID     string
	Status       AgentStatus
	LatestOutput string
	IsError      bool
	At           time.Time
}
