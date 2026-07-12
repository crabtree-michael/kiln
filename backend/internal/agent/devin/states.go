package devin

import "strings"

// Devin's `status_enum` value set is documented loosely and moves (the API is
// young), so — exactly like the amika adapter's sandbox-state handling (05 §6,
// §11) — this file classifies defensively: known strings map to a phase, and an
// unknown value falls through to the safe default (keep polling). This is the one
// place to harden as the real value set is observed against a live Devin.
//
// The mapping's load-bearing subtlety: Devin's `blocked` means "the agent finished
// its work and is waiting for the user" — a COMPLETED turn from Kiln's point of
// view, the analogue of an assistant message appearing in Amika's transcript. It
// is NOT an error. `expired` is the terminal, unrecoverable end of a session; a
// continuation into an expired session is a lost conversation (05 §3).

// turnPhase is the defensive classification of a Devin session's status_enum.
type turnPhase int

const (
	// devinRunning: the agent is still working — keep polling (the safe default
	// for any unrecognised status_enum).
	devinRunning turnPhase = iota
	// devinDone: the agent produced a turn and is at rest (blocked-on-user,
	// finished, stopped) — a completed turn to surface as output.
	devinDone
	// devinExpired: the session is terminally gone; a continuation into it is a
	// lost conversation (agent.ErrConversationLost).
	devinExpired
	// devinErrored: the session hit a terminal failure state.
	devinErrored
)

var (
	// running/transitional statuses — the agent is mid-turn.
	devinRunningStates = set(
		"running", "working", "in_progress", "inprogress", "processing",
		"initializing", "starting", "pending", "queued", "resuming", "created",
	)
	// done statuses — the turn produced its output and the agent is at rest.
	// "blocked" is Devin's "waiting for the user" (a completed turn), NOT an error.
	devinDoneStates = set(
		"blocked", "waiting_for_user", "waiting", "finished", "completed",
		"complete", "done", "stopped", "suspended", "idle", "paused",
	)
	// expired statuses — the session is terminally gone; a continuation is lost.
	devinExpiredStates = set(
		"expired", "terminated", "deleted", "gone",
	)
	// errored statuses — a terminal failure of the session.
	devinErroredStates = set(
		"error", "errored", "failed", "failure", "cancelled", "canceled", "aborted",
	)
)

// classifyStatus maps a Devin status_enum onto a neutral turn phase. An
// unrecognised value is treated as still-running so the poller keeps waiting
// rather than completing a turn on a status it doesn't understand.
func classifyStatus(status string) turnPhase {
	switch s := norm(status); {
	case devinDoneStates[s]:
		return devinDone
	case devinExpiredStates[s]:
		return devinExpired
	case devinErroredStates[s]:
		return devinErrored
	case devinRunningStates[s]:
		return devinRunning
	default: // unknown ⇒ keep polling (safe default)
		return devinRunning
	}
}

// acuUsed returns the session's consumed-ACU figure, tolerating the several
// field names Devin's evolving API has used (acu_used / acu_consumed /
// acu_usage) by taking the first that is set. A hardening point: confirm the
// real field against a live session and drop the rest (design §..., ACU→USD is
// a best-effort estimate only).
func acuUsed(d sessionDetail) float64 {
	switch {
	case d.ACUUsed > 0:
		return d.ACUUsed
	case d.ACUConsumed > 0:
		return d.ACUConsumed
	case d.ACUUsage > 0:
		return d.ACUUsage
	default:
		return 0
	}
}

func norm(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func set(vs ...string) map[string]bool {
	m := make(map[string]bool, len(vs))
	for _, v := range vs {
		m[v] = true
	}
	return m
}
