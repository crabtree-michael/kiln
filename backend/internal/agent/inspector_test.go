package agent_test

// Inspector-seam tests for Service.ListAgents / Service.GetAgentUpdates (05
// §2, §5). These reuse the shared fakes in fakes_test.go (fakeStore,
// fakeSlots, fakeClock, fakeEvents) rather than redeclaring lookalikes — the
// package already has a fully-fledged agent.Store fake keyed by
// idempotency_key, with a seed() helper for inserting rows without driving
// Send/Record.

import (
	"context"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
	"github.com/crabtree-michael/kiln/backend/internal/agent/mock"
	"github.com/crabtree-michael/kiln/backend/internal/testutil"
)

func newInspector(t *testing.T, store *fakeStore) (*agent.Service, *mock.Provider) {
	t.Helper()
	p := mock.New()
	svc := agent.NewService(store, p, &fakeEvents{}, &fakeSlots{}, testutil.NewFakeClock(), nil)
	return svc, p
}

func TestListAgents_ReportsBusyAndIdle(t *testing.T) {
	busy := "11111111-1111-1111-1111-111111111111"
	idle := "22222222-2222-2222-2222-222222222222"
	store := newFakeStore()
	store.seed(agent.Turn{
		IdempotencyKey: 1, Kind: agent.KindSend, WorkerID: busy,
		TicketID: "tkt-a", Phase: agent.PhaseTurnStarted,
	})
	store.seed(agent.Turn{
		IdempotencyKey: 2, Kind: agent.KindSend, WorkerID: idle,
		TicketID: "tkt-b", Phase: agent.PhaseDone,
	})
	svc, p := newInspector(t, store)
	// Make both workers live in the provider.
	mustCreate(t, p, agent.WorkerName(busy))
	mustCreate(t, p, agent.WorkerName(idle))

	got, err := svc.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	byID := map[string]agent.AgentInfo{}
	for _, a := range got {
		byID[a.WorkerID] = a
	}
	if a := byID[busy]; a.Status != agent.AgentBuilding || a.TicketID != "tkt-a" {
		t.Errorf("busy worker = %+v, want working on tkt-a", a)
	}
	if a := byID[idle]; a.Status != agent.AgentIdle || a.TicketID != "tkt-b" {
		t.Errorf("idle worker = %+v, want idle on tkt-b", a)
	}
}

// Liveness dominates turn activity: a worker with a turn recorded as in-flight
// but whose sandbox has stopped/errored/is-still-starting must report that real
// session state, not "building" (amended 2026-07-05) — this is the exact bug
// where a dead sandbox kept showing as building.
func TestListAgents_LivenessDominatesStaleTurn(t *testing.T) {
	cases := []struct {
		name string
		run  agent.RunStatus
		want agent.AgentStatus
	}{
		{"stopped", agent.RunStopped, agent.AgentStopped},
		{"errored", agent.RunErrored, agent.AgentErrored},
		{"starting", agent.RunStarting, agent.AgentStarting},
		{"ready still builds", agent.RunReady, agent.AgentBuilding},
	}
	const boundTicket = "tkt-live"
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeStore()
			store.seed(agent.Turn{
				IdempotencyKey: 1, Kind: agent.KindSend, WorkerID: testWorkerID,
				TicketID: boundTicket, Phase: agent.PhaseTurnStarted, // a turn is "in flight"
			})
			svc, p := newInspector(t, store)
			name := agent.WorkerName(testWorkerID)
			mustCreate(t, p, name)
			p.SetWorkerStatus(name, tc.run)

			got, err := svc.ListAgents(context.Background())
			if err != nil {
				t.Fatalf("ListAgents: %v", err)
			}
			if len(got) != 1 || got[0].Status != tc.want {
				t.Errorf("status = %+v, want %s (liveness %s dominates the in-flight turn)",
					got, tc.want, tc.run)
			}
			// The ticket binding is still reported regardless of liveness.
			if got[0].TicketID != boundTicket {
				t.Errorf("ticket binding = %q, want %q", got[0].TicketID, boundTicket)
			}
		})
	}
}

func TestGetAgentUpdates_ReturnsLatestOutput(t *testing.T) {
	id := "33333333-3333-3333-3333-333333333333"
	store := newFakeStore()
	store.seed(agent.Turn{
		IdempotencyKey: 1, Kind: agent.KindSend, WorkerID: id,
		TicketID: "tkt-c", Phase: agent.PhaseTurnStarted,
	})
	svc, p := newInspector(t, store)
	name := agent.WorkerName(id)
	mustCreate(t, p, name)
	p.SeedLatestOutput(name, agent.TurnOutput{Output: "all done"})

	u, err := svc.GetAgentUpdates(context.Background(), id)
	if err != nil {
		t.Fatalf("GetAgentUpdates: %v", err)
	}
	if u.LatestOutput != "all done" || u.Status != agent.AgentBuilding {
		t.Errorf("update = %+v, want output 'all done' and working", u)
	}
}

func TestGetAgentUpdates_FailedTurnSetsIsError(t *testing.T) {
	id := "44444444-4444-4444-4444-444444444444"
	store := newFakeStore()
	store.seed(agent.Turn{
		IdempotencyKey: 1, Kind: agent.KindSend, WorkerID: id,
		TicketID: "tkt-d", Phase: agent.PhaseFailed,
	})
	svc, p := newInspector(t, store)
	mustCreate(t, p, agent.WorkerName(id))

	u, err := svc.GetAgentUpdates(context.Background(), id)
	if err != nil {
		t.Fatalf("GetAgentUpdates: %v", err)
	}
	if !u.IsError || u.Status != agent.AgentIdle {
		t.Errorf("update = %+v, want IsError and idle", u)
	}
}

func TestGetAgentUpdates_UnknownWorkerIsEmptyNotError(t *testing.T) {
	svc, _ := newInspector(t, newFakeStore())
	u, err := svc.GetAgentUpdates(context.Background(), "nope")
	if err != nil {
		t.Fatalf("GetAgentUpdates: %v", err)
	}
	if u.LatestOutput != "" || u.Status != agent.AgentIdle {
		t.Errorf("update = %+v, want empty idle", u)
	}
}

func mustCreate(t *testing.T, p *mock.Provider, name string) {
	t.Helper()
	if _, err := p.CreateWorker(context.Background(), name); err != nil {
		t.Fatalf("CreateWorker(%s): %v", name, err)
	}
}
