package board_test

// Transition-operation tests for the Board API (03 §4): every operation's
// precondition, state change, return value, and emissions; strict
// error-not-no-op semantics for invalid/repeated transitions (D8); and that
// no edge outside the §2.1 diagram is reachable (D10). Driven against
// fakeStore/fakeTx (fakes_test.go) — no agent-runtime fake needed, since
// asserting appended Emissions *is* asserting the side effect (03 §9).

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/board"
)

func newTestService() (*board.Service, *fakeStore) {
	store := newFakeStore()
	return board.NewService(store), store
}

// ---- CreateTicket (03 §4) ---------------------------------------------

func TestCreateTicket_Success(t *testing.T) {
	svc, store := newTestService()
	ctx := context.Background()

	got, err := svc.CreateTicket(ctx, "Write tests", "Body details")
	if err != nil {
		t.Fatalf("CreateTicket: unexpected error: %v", err)
	}
	if got.ID == "" {
		t.Error("CreateTicket must return a persisted ticket with a non-empty ID")
	}
	if got.Title != "Write tests" || got.Body != "Body details" {
		t.Errorf("CreateTicket returned Title/Body = %q/%q, want %q/%q", got.Title, got.Body, "Write tests", "Body details")
	}
	if got.State != board.StateShaping {
		t.Errorf("new ticket state = %q, want %q (03 §2.1)", got.State, board.StateShaping)
	}
	if got.WorkerID != nil {
		t.Error("a shaping ticket must not have a bound worker (03 I3)")
	}
	if got.BlockedReason != nil {
		t.Error("a shaping ticket must not have a blocked reason (03 I4)")
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Error("CreateTicket must return timestamps, not zero values")
	}

	if _, ok := store.ticket(got.ID); !ok {
		t.Error("CreateTicket must persist the ticket in the store")
	}
}

func TestCreateTicket_EmitsBoardUpdatedAndFeedUpdated(t *testing.T) {
	svc, store := newTestService()
	ctx := context.Background()

	if _, err := svc.CreateTicket(ctx, "T", ""); err != nil {
		t.Fatalf("CreateTicket: unexpected error: %v", err)
	}
	ems := store.outboxSnapshot()
	if len(emissionsWithTopic(ems, board.TopicBoardUpdated)) != 1 {
		t.Errorf("CreateTicket must emit exactly one board.updated, got emissions: %+v", ems)
	}
	// A ticket is created in shaping, and every shaping ticket is a proposal
	// card (08 §5, superseding D5), so CreateTicket also refreshes the feed.
	if len(emissionsWithTopic(ems, board.TopicFeedUpdated)) != 1 {
		t.Errorf("CreateTicket must emit exactly one feed.updated, got emissions: %+v", ems)
	}
	for _, e := range ems {
		if e.Topic != board.TopicBoardUpdated && e.Topic != board.TopicFeedUpdated {
			t.Errorf("CreateTicket must not emit %q (only board.updated + feed.updated)", e.Topic)
		}
	}
}

func TestCreateTicket_EmptyTitle_Rejected(t *testing.T) {
	svc, store := newTestService()
	ctx := context.Background()

	_, err := svc.CreateTicket(ctx, "", "body")
	if err == nil {
		t.Fatal("CreateTicket with empty title must fail (03 §4 precondition: title non-empty)")
	}
	if len(store.tickets) != 0 {
		t.Error("a rejected CreateTicket must not persist any ticket")
	}
	if len(store.outboxSnapshot()) != 0 {
		t.Error("a rejected CreateTicket must not emit anything (03 I7: no partial writes)")
	}
}

// ---- ShapeTicket (03 §4) -----------------------------------------------

func TestShapeTicket_WhileShaping_UpdatesFields(t *testing.T) {
	svc, store := newTestService()
	ctx := context.Background()
	store.seedTicket(board.Ticket{ID: "t1", Title: "old", Body: "old body", State: board.StateShaping, Priority: 0})

	got, err := svc.ShapeTicket(ctx, "t1", board.ShapePatch{Title: new("new"), Body: new("new body"), Priority: new(5)})
	if err != nil {
		t.Fatalf("ShapeTicket: unexpected error: %v", err)
	}
	if got.Title != "new" || got.Body != "new body" || got.Priority != 5 {
		t.Errorf("ShapeTicket did not apply patch: got %+v", got)
	}
	if got.State != board.StateShaping {
		t.Errorf("ShapeTicket must not change state, got %q", got.State)
	}
	// Reshaping a shaping ticket changes its proposal card, so the feed must
	// reassemble (08 §5, superseding D5).
	ems := store.outboxSnapshot()
	if len(emissionsWithTopic(ems, board.TopicFeedUpdated)) != 1 {
		t.Errorf("ShapeTicket on a shaping ticket must emit exactly one feed.updated, got: %+v", ems)
	}
}

func TestShapeTicket_WhileReady_StateUnchanged(t *testing.T) {
	svc, store := newTestService()
	ctx := context.Background()
	store.seedTicket(board.Ticket{ID: "t1", Title: "old", State: board.StateReady, ReadyAt: new(store.now())})

	got, err := svc.ShapeTicket(ctx, "t1", board.ShapePatch{Priority: new(9)})
	if err != nil {
		t.Fatalf("ShapeTicket on a ready ticket: unexpected error: %v (03 §4: state ∈ {shaping, ready})", err)
	}
	if got.State != board.StateReady {
		t.Errorf("ShapeTicket must leave state unchanged, got %q", got.State)
	}
	if got.Priority != 9 {
		t.Errorf("Priority = %d, want 9 (03 §4: no separate Reprioritize op)", got.Priority)
	}
	// A ready ticket has no feed surface, so shaping it emits board.updated only.
	ems := store.outboxSnapshot()
	if len(emissionsWithTopic(ems, board.TopicFeedUpdated)) != 0 {
		t.Errorf("ShapeTicket on a ready ticket must not emit feed.updated, got: %+v", ems)
	}
}

func TestShapeTicket_NilFieldsLeftUnchanged(t *testing.T) {
	svc, store := newTestService()
	ctx := context.Background()
	store.seedTicket(board.Ticket{
		ID: "t1", Title: "keep-title", Body: "keep-body", State: board.StateShaping, Priority: 3,
	})

	got, err := svc.ShapeTicket(ctx, "t1", board.ShapePatch{Priority: new(7)})
	if err != nil {
		t.Fatalf("ShapeTicket: unexpected error: %v", err)
	}
	if got.Title != "keep-title" || got.Body != "keep-body" {
		t.Errorf("nil ShapePatch fields must be left unchanged, got Title=%q Body=%q", got.Title, got.Body)
	}
	if got.Priority != 7 {
		t.Errorf("Priority = %d, want 7", got.Priority)
	}
}

func TestShapeTicket_NotFound(t *testing.T) {
	svc, _ := newTestService()
	_, err := svc.ShapeTicket(context.Background(), "missing", board.ShapePatch{})
	if !errors.Is(err, board.ErrNotFound) {
		t.Fatalf("ShapeTicket on unknown id: err = %v, want ErrNotFound", err)
	}
}

func TestShapeTicket_RejectsNonBacklogStates(t *testing.T) {
	cases := []struct {
		name  string
		state board.State
	}{
		{"working", board.StateWorking},
		{"blocked", board.StateBlocked},
		{"done", board.StateDone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, store := newTestService()
			ctx := context.Background()
			seedActiveOrDoneTicket(store, tc.state)

			_, err := svc.ShapeTicket(ctx, "t1", board.ShapePatch{Title: new("x")})
			requireInvalidTransition(t, err, tc.state, "ShapeTicket")

			if len(store.outboxSnapshot()) != 0 {
				t.Error("a rejected ShapeTicket must not emit anything (03 I7)")
			}
		})
	}
}

// ---- MarkReady (03 §4) --------------------------------------------------

func TestMarkReady_FromShaping_SetsReadyAndReadyAt(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()
	created, err := svc.CreateTicket(ctx, "T", "")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	got, err := svc.MarkReady(ctx, created.ID)
	if err != nil {
		t.Fatalf("MarkReady: unexpected error: %v", err)
	}
	if got.State != board.StateReady {
		t.Errorf("state = %q, want ready", got.State)
	}
	if got.ReadyAt == nil {
		t.Error("MarkReady must set ReadyAt (03 §4)")
	}
}

func TestMarkReady_EmitsPullEvaluateAndBoardUpdated(t *testing.T) {
	svc, store := newTestService()
	ctx := context.Background()
	// Seed via the fixture path (bypasses the outbox) so only MarkReady's own
	// emissions are asserted here; using svc.CreateTicket for setup would add
	// its own board.updated and double-count it (03 §4: every mutation emits
	// board.updated).
	store.seedTicket(board.Ticket{ID: "t1", Title: "T", State: board.StateShaping})

	if _, err := svc.MarkReady(ctx, "t1"); err != nil {
		t.Fatalf("MarkReady: unexpected error: %v", err)
	}
	ems := store.outboxSnapshot()
	if len(emissionsWithTopic(ems, board.TopicPullEvaluate)) != 1 {
		t.Errorf("MarkReady must emit exactly one pull.evaluate, got: %+v", ems)
	}
	if len(emissionsWithTopic(ems, board.TopicBoardUpdated)) != 1 {
		t.Errorf("MarkReady must emit exactly one board.updated, got: %+v", ems)
	}
}

func TestMarkReady_RejectsNonShapingStates(t *testing.T) {
	cases := []board.State{board.StateReady, board.StateWorking, board.StateBlocked, board.StateDone}
	for _, state := range cases {
		t.Run(string(state), func(t *testing.T) {
			svc, store := newTestService()
			seedActiveOrDoneTicket(store, state)

			_, err := svc.MarkReady(context.Background(), "t1")
			requireInvalidTransition(t, err, state, "MarkReady")
		})
	}
}

func TestMarkReady_NotFound(t *testing.T) {
	svc, _ := newTestService()
	_, err := svc.MarkReady(context.Background(), "missing")
	if !errors.Is(err, board.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// ---- SendToAgent (03 §4) — covers both diagram edges --------------------

func TestSendToAgent_FromBlocked_ResumesToWorking(t *testing.T) {
	svc, store := newTestService()
	worker := board.WorkerID("w1")
	store.seedWorker(worker)
	store.seedTicket(board.Ticket{
		ID: "t1", Title: "T", State: board.StateBlocked,
		WorkerID: &worker, BlockedReason: new("needs a decision"),
	})

	got, err := svc.SendToAgent(context.Background(), "t1", "here's the answer")
	if err != nil {
		t.Fatalf("SendToAgent (resume): unexpected error: %v", err)
	}
	if got.State != board.StateWorking {
		t.Errorf("state = %q, want working (03 §2.1 blocked -> working)", got.State)
	}
	if got.BlockedReason != nil {
		t.Error("SendToAgent must clear BlockedReason on resume (03 §4)")
	}
	if got.WorkerID == nil || *got.WorkerID != worker {
		t.Error("SendToAgent must preserve the bound worker")
	}
}

func TestSendToAgent_FromWorking_NewTurn(t *testing.T) {
	svc, store := newTestService()
	worker := board.WorkerID("w1")
	store.seedWorker(worker)
	store.seedTicket(board.Ticket{ID: "t1", Title: "T", State: board.StateWorking, WorkerID: &worker})

	got, err := svc.SendToAgent(context.Background(), "t1", "keep going")
	if err != nil {
		t.Fatalf("SendToAgent (new turn): unexpected error: %v", err)
	}
	if got.State != board.StateWorking {
		t.Errorf("state = %q, want working (03 §2.1 working -> working)", got.State)
	}
}

// A Working→Working nudge is a same-state mutation: it bumps updated_at but must
// leave state_changed_at (the "time in status" clock) untouched, so the client's
// ticket-row age subtext keeps accumulating through nudges instead of resetting.
func TestSendToAgent_FromWorking_PreservesStateChangedAt(t *testing.T) {
	svc, store := newTestService()
	worker := board.WorkerID("w1")
	store.seedWorker(worker)
	entered := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	store.seedTicket(board.Ticket{
		ID: "t1", Title: "T", State: board.StateWorking, WorkerID: &worker,
		StateChangedAt: entered,
	})

	got, err := svc.SendToAgent(context.Background(), "t1", "keep going")
	if err != nil {
		t.Fatalf("SendToAgent (nudge): unexpected error: %v", err)
	}
	if !got.StateChangedAt.Equal(entered) {
		t.Errorf("StateChangedAt = %v, want unchanged %v — a same-state nudge must not reset the clock",
			got.StateChangedAt, entered)
	}
	if !got.UpdatedAt.After(entered) {
		t.Errorf("UpdatedAt = %v, want advanced past %v — a nudge is still a mutation", got.UpdatedAt, entered)
	}
}

// A Blocked→Working resume is a real transition: it must advance state_changed_at
// so the timer restarts from when the ticket re-entered Working.
func TestSendToAgent_FromBlocked_AdvancesStateChangedAt(t *testing.T) {
	svc, store := newTestService()
	worker := board.WorkerID("w1")
	store.seedWorker(worker)
	entered := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	store.seedTicket(board.Ticket{
		ID: "t1", Title: "T", State: board.StateBlocked,
		WorkerID: &worker, BlockedReason: new("needs a decision"),
		StateChangedAt: entered,
	})

	got, err := svc.SendToAgent(context.Background(), "t1", "here's the answer")
	if err != nil {
		t.Fatalf("SendToAgent (resume): unexpected error: %v", err)
	}
	if !got.StateChangedAt.After(entered) {
		t.Errorf("StateChangedAt = %v, want advanced past %v — a real transition restarts the clock",
			got.StateChangedAt, entered)
	}
}

func TestSendToAgent_EmitsAgentSendWithInstruction(t *testing.T) {
	svc, store := newTestService()
	worker := board.WorkerID("w1")
	store.seedWorker(worker)
	store.seedTicket(board.Ticket{ID: "t1", Title: "T", State: board.StateWorking, WorkerID: &worker})

	if _, err := svc.SendToAgent(context.Background(), "t1", "do the next thing"); err != nil {
		t.Fatalf("SendToAgent: unexpected error: %v", err)
	}
	ems := store.outboxSnapshot()
	sends := emissionsWithTopic(ems, board.TopicAgentSend)
	if len(sends) != 1 {
		t.Fatalf("SendToAgent must emit exactly one agent.send, got: %+v", ems)
	}
	payload, ok := sends[0].Payload.(board.SendPayload)
	if !ok {
		t.Fatalf("agent.send payload type = %T, want board.SendPayload", sends[0].Payload)
	}
	if payload.TicketID != "t1" || payload.WorkerID != worker || payload.Message != "do the next thing" {
		t.Errorf("agent.send payload = %+v, want {t1 %s do the next thing}", payload, worker)
	}
}

func TestSendToAgent_RejectsNonActiveStates(t *testing.T) {
	cases := []board.State{board.StateShaping, board.StateReady, board.StateDone}
	for _, state := range cases {
		t.Run(string(state), func(t *testing.T) {
			svc, store := newTestService()
			seedActiveOrDoneTicket(store, state)

			_, err := svc.SendToAgent(context.Background(), "t1", "instruction")
			requireInvalidTransition(t, err, state, "SendToAgent")
		})
	}
}

func TestSendToAgent_NotFound(t *testing.T) {
	svc, _ := newTestService()
	_, err := svc.SendToAgent(context.Background(), "missing", "x")
	if !errors.Is(err, board.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// ---- MarkBlocked (03 §4) -------------------------------------------------

func TestMarkBlocked_FromWorking_SetsBlockedAndReason(t *testing.T) {
	svc, store := newTestService()
	worker := board.WorkerID("w1")
	store.seedWorker(worker)
	store.seedTicket(board.Ticket{ID: "t1", Title: "T", State: board.StateWorking, WorkerID: &worker})

	got, err := svc.MarkBlocked(context.Background(), "t1", "needs a decision from the user")
	if err != nil {
		t.Fatalf("MarkBlocked: unexpected error: %v", err)
	}
	if got.State != board.StateBlocked {
		t.Errorf("state = %q, want blocked", got.State)
	}
	if got.BlockedReason == nil || *got.BlockedReason != "needs a decision from the user" {
		t.Errorf("BlockedReason = %v, want set to the given reason (03 I4)", got.BlockedReason)
	}
	if got.WorkerID == nil || *got.WorkerID != worker {
		t.Error("MarkBlocked must keep the worker bound (03 I3: blocked is still active)")
	}
}

func TestMarkBlocked_EmitsNotifySend(t *testing.T) {
	svc, store := newTestService()
	worker := board.WorkerID("w1")
	store.seedWorker(worker)
	store.seedTicket(board.Ticket{ID: "t1", Title: "Ticket title", State: board.StateWorking, WorkerID: &worker})

	if _, err := svc.MarkBlocked(context.Background(), "t1", "dispatch failure: timeout"); err != nil {
		t.Fatalf("MarkBlocked: unexpected error: %v", err)
	}
	ems := store.outboxSnapshot()
	notifies := emissionsWithTopic(ems, board.TopicNotifySend)
	if len(notifies) != 1 {
		t.Fatalf("MarkBlocked must emit exactly one notify.send, got: %+v", ems)
	}
	payload, ok := notifies[0].Payload.(board.NotifyPayload)
	if !ok {
		t.Fatalf("notify.send payload type = %T, want board.NotifyPayload", notifies[0].Payload)
	}
	if payload.TicketID != "t1" || payload.Reason != "dispatch failure: timeout" {
		t.Errorf("notify.send payload = %+v", payload)
	}
}

// D8: "an already-blocked ticket being re-blocked is an error, not a no-op" — verbatim spec example.
func TestMarkBlocked_AlreadyBlocked_Rejected(t *testing.T) {
	svc, store := newTestService()
	worker := board.WorkerID("w1")
	store.seedWorker(worker)
	store.seedTicket(board.Ticket{
		ID: "t1", Title: "T", State: board.StateBlocked,
		WorkerID: &worker, BlockedReason: new("already blocked"),
	})

	_, err := svc.MarkBlocked(context.Background(), "t1", "trying to block again")
	requireInvalidTransition(t, err, board.StateBlocked, "MarkBlocked")
}

func TestMarkBlocked_RejectsNonWorkingStates(t *testing.T) {
	cases := []board.State{board.StateShaping, board.StateReady, board.StateDone}
	for _, state := range cases {
		t.Run(string(state), func(t *testing.T) {
			svc, store := newTestService()
			seedActiveOrDoneTicket(store, state)

			_, err := svc.MarkBlocked(context.Background(), "t1", "reason")
			requireInvalidTransition(t, err, state, "MarkBlocked")
		})
	}
}

func TestMarkBlocked_NotFound(t *testing.T) {
	svc, _ := newTestService()
	_, err := svc.MarkBlocked(context.Background(), "missing", "reason")
	if !errors.Is(err, board.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// ---- AcceptToDone (03 §4) -------------------------------------------------

func TestAcceptToDone_FromWorking_ClearsWorkerAndReason(t *testing.T) {
	svc, store := newTestService()
	ctx := context.Background()
	worker := board.WorkerID("w1")
	store.seedWorker(worker)
	store.seedTicket(board.Ticket{ID: "t1", Title: "T", State: board.StateWorking, WorkerID: &worker})

	got, err := svc.AcceptToDone(ctx, "t1")
	if err != nil {
		t.Fatalf("AcceptToDone: unexpected error: %v", err)
	}
	if got.State != board.StateDone {
		t.Errorf("state = %q, want done", got.State)
	}
	if got.WorkerID != nil {
		t.Error("AcceptToDone must clear WorkerID (03 §4, I3)")
	}
	if got.BlockedReason != nil {
		t.Error("AcceptToDone must clear BlockedReason")
	}
}

func TestAcceptToDone_FromBlocked_ClearsWorkerAndReason(t *testing.T) {
	svc, store := newTestService()
	worker := board.WorkerID("w1")
	store.seedWorker(worker)
	store.seedTicket(board.Ticket{
		ID: "t1", Title: "T", State: board.StateBlocked,
		WorkerID: &worker, BlockedReason: new("decision needed"),
	})

	got, err := svc.AcceptToDone(context.Background(), "t1")
	if err != nil {
		t.Fatalf("AcceptToDone: unexpected error: %v", err)
	}
	if got.State != board.StateDone {
		t.Errorf("state = %q, want done", got.State)
	}
	if got.WorkerID != nil || got.BlockedReason != nil {
		t.Errorf("AcceptToDone must clear worker+reason, got WorkerID=%v BlockedReason=%v", got.WorkerID, got.BlockedReason)
	}
}

func TestAcceptToDone_FreesTheWorker(t *testing.T) {
	svc, store := newTestService()
	worker := board.WorkerID("w1")
	store.seedWorker(worker)
	store.seedTicket(board.Ticket{ID: "t1", Title: "T", State: board.StateWorking, WorkerID: &worker})

	if _, err := svc.AcceptToDone(context.Background(), "t1"); err != nil {
		t.Fatalf("AcceptToDone: unexpected error: %v", err)
	}
	snap, err := svc.GetBoard(context.Background())
	if err != nil {
		t.Fatalf("GetBoard: unexpected error: %v", err)
	}
	if snap.WorkerFree != 1 {
		t.Errorf("WorkerFree = %d, want 1 after releasing the only worker (03 D2: busy is derived)", snap.WorkerFree)
	}
}

func TestAcceptToDone_EmitsPullEvaluateAndAgentRelease(t *testing.T) {
	svc, store := newTestService()
	worker := board.WorkerID("w1")
	store.seedWorker(worker)
	store.seedTicket(board.Ticket{ID: "t1", Title: "T", State: board.StateWorking, WorkerID: &worker})

	if _, err := svc.AcceptToDone(context.Background(), "t1"); err != nil {
		t.Fatalf("AcceptToDone: unexpected error: %v", err)
	}
	ems := store.outboxSnapshot()
	if len(emissionsWithTopic(ems, board.TopicPullEvaluate)) != 1 {
		t.Errorf("AcceptToDone must emit exactly one pull.evaluate, got: %+v", ems)
	}
	releases := emissionsWithTopic(ems, board.TopicAgentRelease)
	if len(releases) != 1 {
		t.Fatalf("AcceptToDone must emit exactly one agent.release, got: %+v", ems)
	}
	payload, ok := releases[0].Payload.(board.ReleasePayload)
	if !ok {
		t.Fatalf("agent.release payload type = %T, want board.ReleasePayload", releases[0].Payload)
	}
	if payload.WorkerID != worker {
		t.Errorf("agent.release WorkerID = %q, want %q", payload.WorkerID, worker)
	}
}

// The completion card is emitted by the transition itself (08 §7), so a finished
// ticket always posts a persistent feed card regardless of agent behavior.
func TestAcceptToDone_EmitsFeedCompletion(t *testing.T) {
	svc, store := newTestService()
	worker := board.WorkerID("w1")
	store.seedWorker(worker)
	store.seedTicket(board.Ticket{ID: "t1", Title: "Ship it", State: board.StateWorking, WorkerID: &worker})

	if _, err := svc.AcceptToDone(context.Background(), "t1"); err != nil {
		t.Fatalf("AcceptToDone: unexpected error: %v", err)
	}
	ems := store.outboxSnapshot()
	completions := emissionsWithTopic(ems, board.TopicFeedCompletion)
	if len(completions) != 1 {
		t.Fatalf("AcceptToDone must emit exactly one feed.completion, got: %+v", ems)
	}
	payload, ok := completions[0].Payload.(board.CompletionPayload)
	if !ok {
		t.Fatalf("feed.completion payload type = %T, want board.CompletionPayload", completions[0].Payload)
	}
	if payload.TicketID != "t1" || payload.TicketTitle != "Ship it" {
		t.Errorf("feed.completion payload = %+v, want {t1 Ship it}", payload)
	}
}

// D8: repeated AcceptToDone (done -> done) is also a "reopen" attempt via
// D10 — no such edge exists, and it must reject loudly, not no-op.
func TestAcceptToDone_AlreadyDone_Rejected(t *testing.T) {
	svc, store := newTestService()
	seedActiveOrDoneTicket(store, board.StateDone)

	_, err := svc.AcceptToDone(context.Background(), "t1")
	requireInvalidTransition(t, err, board.StateDone, "AcceptToDone")
}

func TestAcceptToDone_RejectsBacklogStates(t *testing.T) {
	cases := []board.State{board.StateShaping, board.StateReady}
	for _, state := range cases {
		t.Run(string(state), func(t *testing.T) {
			svc, store := newTestService()
			seedActiveOrDoneTicket(store, state)

			_, err := svc.AcceptToDone(context.Background(), "t1")
			requireInvalidTransition(t, err, state, "AcceptToDone")
		})
	}
}

func TestAcceptToDone_NotFound(t *testing.T) {
	svc, _ := newTestService()
	_, err := svc.AcceptToDone(context.Background(), "missing")
	if !errors.Is(err, board.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// ---- D10: no cancel/delete/reopen edges exist ----------------------------
//
// There is no Service method to call for these — that's the point (D10):
// the absence is a compile-time fact, not a runtime one. What we *can* test
// at runtime is that every existing operation refuses to touch a `done`
// ticket (no implicit reopen) and that AcceptToDone can't be invoked twice
// (no implicit re-close/no-op). Those cases are covered above per-operation
// (TestShapeTicket_RejectsNonBacklogStates/done, TestMarkReady_.../done,
// TestSendToAgent_.../done, TestMarkBlocked_.../done,
// TestAcceptToDone_AlreadyDone_Rejected).

// ---- board.updated on every mutation (03 §4) -----------------------------

func TestEveryMutation_EmitsBoardUpdated(t *testing.T) {
	type step struct {
		name string
		run  func(svc *board.Service, store *fakeStore) error
	}
	steps := []step{
		{"CreateTicket", func(svc *board.Service, store *fakeStore) error {
			if _, err := svc.CreateTicket(context.Background(), "T", ""); err != nil {
				return fmt.Errorf("CreateTicket: %w", err)
			}
			return nil
		}},
		{"ShapeTicket", func(svc *board.Service, store *fakeStore) error {
			store.seedTicket(board.Ticket{ID: "t1", Title: "T", State: board.StateShaping})
			if _, err := svc.ShapeTicket(context.Background(), "t1", board.ShapePatch{Priority: new(1)}); err != nil {
				return fmt.Errorf("ShapeTicket: %w", err)
			}
			return nil
		}},
		{"MarkReady", func(svc *board.Service, store *fakeStore) error {
			store.seedTicket(board.Ticket{ID: "t1", Title: "T", State: board.StateShaping})
			if _, err := svc.MarkReady(context.Background(), "t1"); err != nil {
				return fmt.Errorf("MarkReady: %w", err)
			}
			return nil
		}},
		{"SendToAgent", func(svc *board.Service, store *fakeStore) error {
			w := board.WorkerID("w1")
			store.seedWorker(w)
			store.seedTicket(board.Ticket{ID: "t1", Title: "T", State: board.StateWorking, WorkerID: &w})
			if _, err := svc.SendToAgent(context.Background(), "t1", "go"); err != nil {
				return fmt.Errorf("SendToAgent: %w", err)
			}
			return nil
		}},
		{"MarkBlocked", func(svc *board.Service, store *fakeStore) error {
			w := board.WorkerID("w1")
			store.seedWorker(w)
			store.seedTicket(board.Ticket{ID: "t1", Title: "T", State: board.StateWorking, WorkerID: &w})
			if _, err := svc.MarkBlocked(context.Background(), "t1", "reason"); err != nil {
				return fmt.Errorf("MarkBlocked: %w", err)
			}
			return nil
		}},
		{"AcceptToDone", func(svc *board.Service, store *fakeStore) error {
			w := board.WorkerID("w1")
			store.seedWorker(w)
			store.seedTicket(board.Ticket{ID: "t1", Title: "T", State: board.StateWorking, WorkerID: &w})
			if _, err := svc.AcceptToDone(context.Background(), "t1"); err != nil {
				return fmt.Errorf("AcceptToDone: %w", err)
			}
			return nil
		}},
	}

	for _, st := range steps {
		t.Run(st.name, func(t *testing.T) {
			store := newFakeStore()
			svc := board.NewService(store)
			if err := st.run(svc, store); err != nil {
				t.Fatalf("%s: unexpected error: %v", st.name, err)
			}
			ems := store.outboxSnapshot()
			if len(emissionsWithTopic(ems, board.TopicBoardUpdated)) != 1 {
				t.Errorf("%s must emit exactly one board.updated (03 §4), got: %+v", st.name, ems)
			}
		})
	}
}

// ---- helpers --------------------------------------------------------------

// seedActiveOrDoneTicket seeds ticket "t1" in the given state with whatever
// companion fields I3/I4 require, so precondition-rejection tests can target
// every non-eligible from-state without hand-rolling the invariant fields
// each time.
func seedActiveOrDoneTicket(store *fakeStore, state board.State) {
	const id = board.TicketID("t1")
	tk := board.Ticket{ID: id, Title: "T", State: state}
	if state.Active() {
		w := board.WorkerID("w-" + string(id))
		store.seedWorker(w)
		tk.WorkerID = &w
	}
	if state == board.StateBlocked {
		tk.BlockedReason = new("reason")
	}
	if state == board.StateReady {
		rt := store.now()
		tk.ReadyAt = &rt
	}
	store.seedTicket(tk)
}
