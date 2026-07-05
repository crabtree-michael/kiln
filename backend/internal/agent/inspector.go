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

// AgentStatus is the neutral running state the brain and the Streams view see
// (05 §2, amended 2026-07-05). It composes two independent facts: provider
// liveness (RunStatus, from ListWorkers) and turn activity (from agent_turns).
// Liveness dominates — a stopped/errored/starting sandbox reads as such
// regardless of any stale in-flight turn row; only a live worker distinguishes
// building (a turn in flight) from idle. This is what makes a silently
// auto-stopped session visible in Streams without a manual nudge.
//
//nolint:revive // name fixed by 05 §2's list_agents/get_agent_updates contract, not a stutter accident
type AgentStatus string

const (
	AgentBuilding AgentStatus = "building" // alive + a send turn in flight
	AgentIdle     AgentStatus = "idle"     // alive + no turn (done/released/none)
	AgentStopped  AgentStatus = "stopped"  // session auto-stopped / not running
	AgentErrored  AgentStatus = "errored"  // terminal session/provisioning failure
	AgentStarting AgentStatus = "starting" // session provisioning, not reachable yet
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
