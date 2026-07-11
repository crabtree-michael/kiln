package board_test

// 08 §5 feed/activity side effects on the Board API: RequestApproval's proposal
// fact + feed.updated; MarkReady clearing the flag and emitting a queued toast +
// feed.updated; and the per-verb activity toasts (started/nudged/finished) plus
// the blocker's feed.updated. Driven against fakeStore/fakeTx (fakes_test.go) —
// asserting appended Emissions *is* asserting the side effect (03 §9).

import (
	"context"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/board"
)

// toastVerbs returns the Verb of every activity.toast emission, in order.
func toastVerbs(t *testing.T, ems []board.Emission) []string {
	t.Helper()
	toasts := emissionsWithTopic(ems, board.TopicActivityToast)
	verbs := make([]string, 0, len(toasts))
	for _, e := range toasts {
		p, ok := e.Payload.(board.ToastPayload)
		if !ok {
			t.Fatalf("activity.toast payload type = %T, want board.ToastPayload", e.Payload)
		}
		verbs = append(verbs, p.Verb)
	}
	return verbs
}

// soleFeedUpdated returns the single feed.updated emission's descriptor,
// asserting there is exactly one — the runtime uses this to name the change in
// its "all"-mode push (02 §10), so every feed change must carry it.
func soleFeedUpdated(t *testing.T, ems []board.Emission) board.FeedUpdatedPayload {
	t.Helper()
	feeds := emissionsWithTopic(ems, board.TopicFeedUpdated)
	if len(feeds) != 1 {
		t.Fatalf("want exactly one feed.updated, got %d: %+v", len(feeds), ems)
	}
	p, ok := feeds[0].Payload.(board.FeedUpdatedPayload)
	if !ok {
		t.Fatalf("feed.updated payload type = %T, want board.FeedUpdatedPayload", feeds[0].Payload)
	}
	return p
}

// ---- RequestApproval (08 §B.2) ----------------------------------------

func TestRequestApproval_FromShaping_SetsFlagAndEmitsFeedUpdated(t *testing.T) {
	svc, store := newTestService()
	store.seedTicket(projA, board.Ticket{ID: "t1", Title: "Pick a DB", State: board.StateShaping})

	got, err := svc.RequestApproval(context.Background(), projA, "t1")
	if err != nil {
		t.Fatalf("RequestApproval: unexpected error: %v", err)
	}
	if !got.ApprovalRequested {
		t.Error("RequestApproval must set ApprovalRequested = true")
	}
	if got.State != board.StateShaping {
		t.Errorf("state = %q, want shaping (RequestApproval does not move the ticket)", got.State)
	}

	stored, _ := store.ticket("t1")
	if !stored.ApprovalRequested {
		t.Error("RequestApproval must persist ApprovalRequested = true")
	}

	ems := store.outboxSnapshot()
	if len(emissionsWithTopic(ems, board.TopicFeedUpdated)) != 1 {
		t.Errorf("RequestApproval must emit exactly one feed.updated, got: %+v", ems)
	}
	if len(emissionsWithTopic(ems, board.TopicBoardUpdated)) != 1 {
		t.Errorf("RequestApproval must emit exactly one board.updated, got: %+v", ems)
	}
}

func TestRequestApproval_RejectsNonShapingStates(t *testing.T) {
	cases := []board.State{board.StateReady, board.StateWorking, board.StateBlocked, board.StateDone}
	for _, state := range cases {
		t.Run(string(state), func(t *testing.T) {
			svc, store := newTestService()
			seedActiveOrDoneTicket(store, state)

			_, err := svc.RequestApproval(context.Background(), projA, "t1")
			requireInvalidTransition(t, err, state, "RequestApproval")

			if len(store.outboxSnapshot()) != 0 {
				t.Error("a rejected RequestApproval must not emit anything (03 I7)")
			}
			stored, _ := store.ticket("t1")
			if stored.ApprovalRequested {
				t.Error("a rejected RequestApproval must not set the flag")
			}
		})
	}
}

func TestRequestApproval_NotFound(t *testing.T) {
	svc, _ := newTestService()
	_, err := svc.RequestApproval(context.Background(), projA, "missing")
	if err == nil {
		t.Fatal("RequestApproval on unknown id must fail")
	}
}

// ---- MarkReady clears the flag + queued toast + feed.updated (08 §B.3) --

func TestMarkReady_ClearsApprovalRequested(t *testing.T) {
	svc, store := newTestService()
	store.seedTicket(projA, board.Ticket{ID: "t1", Title: "T", State: board.StateShaping, ApprovalRequested: true})

	got, err := svc.MarkReady(context.Background(), projA, "t1")
	if err != nil {
		t.Fatalf("MarkReady: unexpected error: %v", err)
	}
	if got.ApprovalRequested {
		t.Error("MarkReady must clear ApprovalRequested (a queued ticket has no pending proposal)")
	}
	stored, _ := store.ticket("t1")
	if stored.ApprovalRequested {
		t.Error("MarkReady must persist ApprovalRequested = false")
	}
}

func TestMarkReady_EmitsQueuedToastAndFeedUpdated(t *testing.T) {
	svc, store := newTestService()
	store.seedTicket(projA, board.Ticket{ID: "t1", Title: "Ship the thing", State: board.StateShaping})

	if _, err := svc.MarkReady(context.Background(), projA, "t1"); err != nil {
		t.Fatalf("MarkReady: unexpected error: %v", err)
	}
	ems := store.outboxSnapshot()
	if len(emissionsWithTopic(ems, board.TopicFeedUpdated)) != 1 {
		t.Errorf("MarkReady must emit exactly one feed.updated, got: %+v", ems)
	}
	toasts := emissionsWithTopic(ems, board.TopicActivityToast)
	if len(toasts) != 1 {
		t.Fatalf("MarkReady must emit exactly one activity.toast, got: %+v", ems)
	}
	p, ok := toasts[0].Payload.(board.ToastPayload)
	if !ok {
		t.Fatalf("activity.toast payload type = %T, want board.ToastPayload", toasts[0].Payload)
	}
	if p.Verb != "queued" || p.TicketID != "t1" || p.TicketTitle != "Ship the thing" {
		t.Errorf("queued toast payload = %+v, want {queued t1 Ship the thing}", p)
	}
}

// ---- per-verb toasts on the §4 verbs (08 §B.4) -------------------------

func TestPull_Dispatch_EmitsStartedToast(t *testing.T) {
	svc, store := newTestService()
	store.seedWorker(projA, "w1")
	store.seedTicket(projA, board.Ticket{ID: "t1", Title: "Do work", State: board.StateReady, ReadyAt: new(store.now())})

	if err := svc.RunPull(context.Background(), projA); err != nil {
		t.Fatalf("RunPull: unexpected error: %v", err)
	}
	verbs := toastVerbs(t, store.outboxSnapshot())
	if len(verbs) != 1 || verbs[0] != "started" {
		t.Errorf("dispatch must emit exactly one started toast, got verbs: %v", verbs)
	}
}

func TestSendToAgent_ResumeFromBlocked_EmitsNudgedToastAndFeedUpdated(t *testing.T) {
	svc, store := newTestService()
	worker := board.WorkerID("w1")
	store.seedWorker(projA, worker)
	store.seedTicket(projA, board.Ticket{
		ID: "t1", Title: "T", State: board.StateBlocked,
		WorkerID: &worker, BlockedReason: new("needs a decision"),
	})

	if _, err := svc.SendToAgent(context.Background(), projA, "t1", "here's the answer"); err != nil {
		t.Fatalf("SendToAgent: unexpected error: %v", err)
	}
	ems := store.outboxSnapshot()
	if len(emissionsWithTopic(ems, board.TopicFeedUpdated)) != 1 {
		t.Errorf("resume out of Blocked must emit exactly one feed.updated, got: %+v", ems)
	}
	verbs := toastVerbs(t, ems)
	if len(verbs) != 1 || verbs[0] != "nudged" {
		t.Errorf("resume out of Blocked must emit exactly one nudged toast, got verbs: %v", verbs)
	}
}

func TestSendToAgent_NewTurnFromWorking_NoToastNoFeedUpdated(t *testing.T) {
	svc, store := newTestService()
	worker := board.WorkerID("w1")
	store.seedWorker(projA, worker)
	store.seedTicket(projA, board.Ticket{ID: "t1", Title: "T", State: board.StateWorking, WorkerID: &worker})

	if _, err := svc.SendToAgent(context.Background(), projA, "t1", "keep going"); err != nil {
		t.Fatalf("SendToAgent: unexpected error: %v", err)
	}
	ems := store.outboxSnapshot()
	if len(emissionsWithTopic(ems, board.TopicFeedUpdated)) != 0 {
		t.Errorf("a working→working new turn must not emit feed.updated, got: %+v", ems)
	}
	if len(emissionsWithTopic(ems, board.TopicActivityToast)) != 0 {
		t.Errorf("a working→working new turn must not emit an activity.toast, got: %+v", ems)
	}
}

func TestAcceptToDone_EmitsFinishedToastAndFeedUpdated(t *testing.T) {
	svc, store := newTestService()
	worker := board.WorkerID("w1")
	store.seedWorker(projA, worker)
	store.seedTicket(projA, board.Ticket{ID: "t1", Title: "Land it", State: board.StateWorking, WorkerID: &worker})

	if _, err := svc.AcceptToDone(context.Background(), projA, "t1"); err != nil {
		t.Fatalf("AcceptToDone: unexpected error: %v", err)
	}
	ems := store.outboxSnapshot()
	if len(emissionsWithTopic(ems, board.TopicFeedUpdated)) != 1 {
		t.Errorf("AcceptToDone must emit exactly one feed.updated, got: %+v", ems)
	}
	verbs := toastVerbs(t, ems)
	if len(verbs) != 1 || verbs[0] != "finished" {
		t.Errorf("AcceptToDone must emit exactly one finished toast, got verbs: %v", verbs)
	}
}

func TestMarkBlocked_EmitsFeedUpdatedNoToast(t *testing.T) {
	svc, store := newTestService()
	worker := board.WorkerID("w1")
	store.seedWorker(projA, worker)
	store.seedTicket(projA, board.Ticket{ID: "t1", Title: "T", State: board.StateWorking, WorkerID: &worker})

	if _, err := svc.MarkBlocked(context.Background(), projA, "t1", "needs a decision"); err != nil {
		t.Fatalf("MarkBlocked: unexpected error: %v", err)
	}
	ems := store.outboxSnapshot()
	if len(emissionsWithTopic(ems, board.TopicFeedUpdated)) != 1 {
		t.Errorf("MarkBlocked must emit exactly one feed.updated (a blocker card), got: %+v", ems)
	}
	if len(emissionsWithTopic(ems, board.TopicActivityToast)) != 0 {
		t.Errorf("MarkBlocked must not emit an activity.toast (blocker is a feed card), got: %+v", ems)
	}
}

// Every feed.updated names the ticket and the nature of the change, so the
// runtime's "all"-mode push is specific rather than a generic placeholder.
func TestFeedUpdated_CarriesChangeDescriptor(t *testing.T) {
	t.Run("create is a proposal", func(t *testing.T) {
		svc, store := newTestService()
		if _, err := svc.CreateTicket(context.Background(), projA, "Login Redesign", ""); err != nil {
			t.Fatalf("CreateTicket: %v", err)
		}
		p := soleFeedUpdated(t, store.outboxSnapshot())
		if p.Title != "Login Redesign" || p.Verb != board.FeedVerbProposal {
			t.Errorf("descriptor = %+v, want {Login Redesign proposal}", p)
		}
	})

	t.Run("mark ready is queued", func(t *testing.T) {
		svc, store := newTestService()
		store.seedTicket(projA, board.Ticket{ID: "t1", Title: "Queue me", State: board.StateShaping})
		if _, err := svc.MarkReady(context.Background(), projA, "t1"); err != nil {
			t.Fatalf("MarkReady: %v", err)
		}
		p := soleFeedUpdated(t, store.outboxSnapshot())
		if p.Title != "Queue me" || p.Verb != board.FeedVerbQueued {
			t.Errorf("descriptor = %+v, want {Queue me queued}", p)
		}
	})

	t.Run("mark blocked is blocked", func(t *testing.T) {
		svc, store := newTestService()
		worker := board.WorkerID("w1")
		store.seedWorker(projA, worker)
		store.seedTicket(projA, board.Ticket{ID: "t1", Title: "Fix auth", State: board.StateWorking, WorkerID: &worker})
		if _, err := svc.MarkBlocked(context.Background(), projA, "t1", "which provider?"); err != nil {
			t.Fatalf("MarkBlocked: %v", err)
		}
		p := soleFeedUpdated(t, store.outboxSnapshot())
		if p.Title != "Fix auth" || p.Verb != board.FeedVerbBlocked {
			t.Errorf("descriptor = %+v, want {Fix auth blocked}", p)
		}
	})

	t.Run("accept to done is finished", func(t *testing.T) {
		svc, store := newTestService()
		worker := board.WorkerID("w1")
		store.seedWorker(projA, worker)
		store.seedTicket(projA, board.Ticket{ID: "t1", Title: "Deploy done", State: board.StateWorking, WorkerID: &worker})
		if _, err := svc.AcceptToDone(context.Background(), projA, "t1"); err != nil {
			t.Fatalf("AcceptToDone: %v", err)
		}
		p := soleFeedUpdated(t, store.outboxSnapshot())
		if p.Title != "Deploy done" || p.Verb != board.FeedVerbFinished {
			t.Errorf("descriptor = %+v, want {Deploy done finished}", p)
		}
	})
}

// ---- SeedTicket dev seam (08 §B.6) ------------------------------------

func TestSeedTicket_DefaultShaping(t *testing.T) {
	svc, store := newTestService()

	got, err := svc.SeedTicket(context.Background(), projA, board.SeedSpec{Title: "Seed me"})
	if err != nil {
		t.Fatalf("SeedTicket: unexpected error: %v", err)
	}
	if got.State != board.StateShaping {
		t.Errorf("default seed state = %q, want shaping", got.State)
	}
	if got.ApprovalRequested {
		t.Error("a plain shaping seed must not request approval")
	}
	// Every shaping ticket is a proposal card (08 §5, superseding D5), so even
	// a plain shaping seed is a feed surface and must reassemble the feed.
	ems := store.outboxSnapshot()
	if len(emissionsWithTopic(ems, board.TopicFeedUpdated)) != 1 {
		t.Errorf("a plain shaping seed is a proposal card, must emit feed.updated: %+v", ems)
	}
}

func TestSeedTicket_ShapingWithApprovalRequested(t *testing.T) {
	svc, store := newTestService()

	got, err := svc.SeedTicket(context.Background(), projA, board.SeedSpec{
		Title: "Proposal", State: board.StateShaping, ApprovalRequested: true,
	})
	if err != nil {
		t.Fatalf("SeedTicket: unexpected error: %v", err)
	}
	if !got.ApprovalRequested {
		t.Error("SeedTicket must honor ApprovalRequested = true")
	}
	ems := store.outboxSnapshot()
	if len(emissionsWithTopic(ems, board.TopicFeedUpdated)) != 1 {
		t.Errorf("a proposal seed must emit feed.updated, got: %+v", ems)
	}
}

func TestSeedTicket_BlockedBindsWorkerAndReason(t *testing.T) {
	svc, store := newTestService()
	store.seedWorker(projA, "w1")

	got, err := svc.SeedTicket(context.Background(), projA, board.SeedSpec{
		Title: "Blocked one", State: board.StateBlocked, BlockedReason: "which auth?",
	})
	if err != nil {
		t.Fatalf("SeedTicket(blocked): unexpected error: %v", err)
	}
	if got.State != board.StateBlocked {
		t.Errorf("state = %q, want blocked", got.State)
	}
	if got.WorkerID == nil {
		t.Error("a blocked seed must bind a worker (03 I3)")
	}
	if got.BlockedReason == nil || *got.BlockedReason != "which auth?" {
		t.Errorf("BlockedReason = %v, want %q (03 I4)", got.BlockedReason, "which auth?")
	}
	ems := store.outboxSnapshot()
	if len(emissionsWithTopic(ems, board.TopicFeedUpdated)) != 1 {
		t.Errorf("a blocked seed must emit feed.updated (a blocker card), got: %+v", ems)
	}
}

func TestSeedTicket_BlockedNoFreeWorker(t *testing.T) {
	svc, _ := newTestService()
	_, err := svc.SeedTicket(context.Background(), projA, board.SeedSpec{Title: "Blocked", State: board.StateBlocked})
	if err == nil {
		t.Fatal("a blocked seed with no free worker must fail")
	}
}

func TestSeedTicket_EmptyTitleRejected(t *testing.T) {
	svc, _ := newTestService()
	_, err := svc.SeedTicket(context.Background(), projA, board.SeedSpec{Title: ""})
	if err == nil {
		t.Fatal("SeedTicket with empty title must fail")
	}
}
