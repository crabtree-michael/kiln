package agent

// White-box tests for the status composition (amended 2026-07-05): statusFor
// folds provider liveness with turn activity, and statusChanged is the
// liveness-loop diff that decides whether to nudge the board. Both are
// unexported, so these live in package agent.

import "testing"

func TestStatusFor(t *testing.T) {
	cases := []struct {
		run         RunStatus
		turnRunning bool
		want        AgentStatus
	}{
		// A live worker distinguishes building (turn in flight) from idle.
		{RunReady, true, AgentBuilding},
		{RunReady, false, AgentIdle},
		// Liveness dominates: a stale in-flight turn never masks a dead session.
		{RunStopped, true, AgentStopped},
		{RunStopped, false, AgentStopped},
		{RunErrored, true, AgentErrored},
		{RunStarting, true, AgentStarting},
		// An empty RunStatus (a provider that reports no liveness) degrades to the
		// pre-liveness working|idle behaviour.
		{RunStatus(""), true, AgentBuilding},
		{RunStatus(""), false, AgentIdle},
	}
	for _, tc := range cases {
		if got := statusFor(tc.run, tc.turnRunning); got != tc.want {
			t.Errorf("statusFor(%q, running=%v) = %q, want %q", tc.run, tc.turnRunning, got, tc.want)
		}
	}
}

func TestStatusChanged(t *testing.T) {
	s := &Service{}
	info := func(id string, st AgentStatus) AgentInfo { return AgentInfo{WorkerID: id, Status: st} }

	// First observation of any non-empty set is a change (nil → populated).
	if !s.statusChanged([]AgentInfo{info("w1", AgentBuilding)}) {
		t.Fatal("first non-empty status set must count as a change")
	}
	// Same set again: no change.
	if s.statusChanged([]AgentInfo{info("w1", AgentBuilding)}) {
		t.Fatal("an unchanged status set must not count as a change")
	}
	// A status transition (building → stopped) is a change.
	if !s.statusChanged([]AgentInfo{info("w1", AgentStopped)}) {
		t.Fatal("a status transition must count as a change")
	}
	// Adding a worker is a change.
	if !s.statusChanged([]AgentInfo{info("w1", AgentStopped), info("w2", AgentIdle)}) {
		t.Fatal("a new worker must count as a change")
	}
	// Removing a worker is a change.
	if !s.statusChanged([]AgentInfo{info("w1", AgentStopped)}) {
		t.Fatal("a removed worker must count as a change")
	}
}
