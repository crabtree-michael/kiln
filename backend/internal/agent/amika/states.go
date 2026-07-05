package amika

import (
	"math"
	"strings"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
)

// v0beta1 does NOT enumerate sandbox `state` or job `state` values (05 §6,
// §11), so this file classifies them defensively: known strings map to a
// phase, and unknown values fall through to the safe default (keep polling for
// readiness; keep polling a turn unless it produced a result). These sets are
// the one place to harden as the real value set is observed against a live
// Amika (the open question §11 hands to the implementation).

// autoDeleteOff is the interval value that disables Amika's auto-delete (the
// example default in the v0beta1 docs; a negative interval reads as "never").
// Delete must stay exclusively ours (05 D6).
const autoDeleteOff = -1

// autoStopInterval converts KILN_WORKER_AUTO_STOP into Amika's
// auto_stop_interval. v0beta1 does not document the unit, but a live probe
// confirmed it is NOT seconds — a sandbox created with interval=45 stayed
// running well past 45s (2026-07-04), and the Amika UI default is 30 — so whole
// minutes is correct. d <= 0 disables auto-stop (0, matching the docs' example
// default).
func autoStopInterval(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return max(int(math.Round(d.Minutes())), 1)
}

// sandboxPhase is the defensive classification of a sandbox's state (05 §6).
type sandboxPhase int

const (
	sbProvisioning sandboxPhase = iota // creating/starting/unknown — not ready yet, keep polling
	sbReady                            // reachable — can accept a turn
	sbStopped                          // auto-stopped — wake it, then it becomes ready
	sbErrored                          // terminal provisioning failure
)

var (
	sandboxReadyStates = set(
		"running", "ready", "started", "active", "available", "up",
	)
	sandboxStoppedStates = set(
		"stopped", "paused", "suspended", "idle", "asleep", "hibernated",
		"auto_stopped", "autostopped", "sleeping",
	)
	sandboxErroredStates = set(
		"error", "errored", "failed", "failure", "terminated", "deleted", "dead",
	)
	// Provisioning/transitional states ("", pending, creating, provisioning,
	// starting, initializing, booting, queued, building, cloning, setup, …) are
	// the documented-safe default: anything not matched above is treated as
	// not-ready-yet, so classifyState keeps polling rather than guessing.
)

func classifyState(state string) sandboxPhase {
	switch s := norm(state); {
	case sandboxReadyStates[s]:
		return sbReady
	case sandboxStoppedStates[s]:
		return sbStopped
	case sandboxErroredStates[s]:
		return sbErrored
	default: // provisioning or unknown ⇒ not ready, keep polling
		return sbProvisioning
	}
}

// runStatus promotes the internal sandbox phase to the provider-neutral
// agent.RunStatus that ListWorkers surfaces for liveness (05 §2, amended
// 2026-07-05). The same phase WorkerReady consumes as a wake/ready decision is
// now also reported, so a silently auto-stopped sandbox is visible without a
// nudge.
func runStatus(p sandboxPhase) agent.RunStatus {
	switch p {
	case sbReady:
		return agent.RunReady
	case sbStopped:
		return agent.RunStopped
	case sbErrored:
		return agent.RunErrored
	case sbProvisioning:
		return agent.RunStarting
	}
	return agent.RunStarting // unreachable: keeps not-ready-yet the safe default
}

func norm(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func set(vs ...string) map[string]bool {
	m := make(map[string]bool, len(vs))
	for _, v := range vs {
		m[v] = true
	}
	return m
}
