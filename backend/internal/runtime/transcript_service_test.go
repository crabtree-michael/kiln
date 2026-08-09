package runtime_test

// Transcript unit tests (07 §3–§4): the conversation surface — PostMessage's
// transactional ingest, Say's append-then-push ordering, Recent's oldest-first
// tail, and the Nudger hook that wakes the events worker after an ingest. Built
// over the two transcript ports alone: no queue store, no brain, no workers, no
// clock. These assertions used to run through Service (service_test.go), where
// asserting that a failed append never reaches the SSE push meant standing up an
// event dispatcher and a push coordinator first.

import (
	"context"
	"errors"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/runtime"
)

var errStoreFailed = errors.New("fakeMessageStore: synthetic failure")

// fakeNudger records wakeups of the events worker, standing in for the queue
// core that owns it (runtime.Nudger — split plan §4).
type fakeNudger struct{ nudges int }

func (f *fakeNudger) NudgeEvents() { f.nudges++ }

var _ runtime.Nudger = (*fakeNudger)(nil)

// ---- PostMessage (07 §3-§4) ------------------------------------------------

func TestTranscript_PostMessage_DelegatesToMessageStoreAndReturnsBothIDs(t *testing.T) {
	messages := &fakeMessageStore{}
	tx := runtime.NewTranscript(messages, &fakeSayPusher{})

	msgID, evID, err := tx.PostMessage(context.Background(), defaultTestProject, "build the widget")
	if err != nil {
		t.Fatalf("PostMessage: unexpected error: %v", err)
	}
	if messages.appendUserCalls != 1 {
		t.Fatalf("AppendUserMessageAndEnqueueEvent called %d times, want exactly 1", messages.appendUserCalls)
	}
	if msgID == 0 || evID == 0 {
		t.Errorf("PostMessage returned (messageID=%d, eventID=%d), want both non-zero", msgID, evID)
	}
}

// TestTranscript_PostMessage_PropagatesStoreErrorWithoutPartialIDs pins that
// PostMessage does not invent ids or otherwise paper over a failed
// transactional append+enqueue (07 §3: "the transcript and the event queue
// cannot disagree" — a failure here must be visible, not silently partial).
func TestTranscript_PostMessage_PropagatesStoreErrorWithoutPartialIDs(t *testing.T) {
	messages := &fakeMessageStore{
		appendUserFn: func(context.Context, string, string) (int64, int64, error) {
			return 0, 0, errStoreFailed
		},
	}
	tx := runtime.NewTranscript(messages, &fakeSayPusher{})

	msgID, evID, err := tx.PostMessage(context.Background(), defaultTestProject, "hello")
	if !errors.Is(err, errStoreFailed) {
		t.Fatalf("PostMessage error = %v, want errStoreFailed", err)
	}
	if msgID != 0 || evID != 0 {
		t.Errorf("PostMessage on failure returned (messageID=%d, eventID=%d), want (0,0)", msgID, evID)
	}
}

// ---- the Nudger hook (04 §5, split plan §4) --------------------------------

// A committed ingest wakes the events worker through the hook, rather than the
// transcript holding the worker itself.
func TestTranscript_PostMessage_NudgesTheEventsWorker(t *testing.T) {
	nudger := &fakeNudger{}
	tx := runtime.NewTranscript(&fakeMessageStore{}, &fakeSayPusher{})
	tx.SetNudger(nudger)

	if _, _, err := tx.PostMessage(context.Background(), defaultTestProject, "hi"); err != nil {
		t.Fatalf("PostMessage: %v", err)
	}
	if nudger.nudges != 1 {
		t.Errorf("NudgeEvents called %d times, want exactly 1 per committed ingest (04 §5)", nudger.nudges)
	}
}

// A failed ingest has nothing to wake the worker for: the nudge is the commit's
// consequence, not the call's.
func TestTranscript_PostMessage_DoesNotNudgeWhenAppendFails(t *testing.T) {
	nudger := &fakeNudger{}
	tx := runtime.NewTranscript(&fakeMessageStore{
		appendUserFn: func(context.Context, string, string) (int64, int64, error) {
			return 0, 0, errStoreFailed
		},
	}, &fakeSayPusher{})
	tx.SetNudger(nudger)

	if _, _, err := tx.PostMessage(context.Background(), defaultTestProject, "hi"); !errors.Is(err, errStoreFailed) {
		t.Fatalf("PostMessage error = %v, want errStoreFailed", err)
	}
	if nudger.nudges != 0 {
		t.Errorf("NudgeEvents called %d times after a failed append, want 0", nudger.nudges)
	}
}

// Before wiring closes the ingest→nudge edge there is no worker to wake, and an
// ingest must still succeed — the worker's poll fallback catches the row, so a
// missing nudge costs latency, not the event (Nudger's nil contract).
func TestTranscript_PostMessage_SucceedsWithNoNudgerWired(t *testing.T) {
	messages := &fakeMessageStore{}
	tx := runtime.NewTranscript(messages, &fakeSayPusher{})

	if _, _, err := tx.PostMessage(context.Background(), defaultTestProject, "hi"); err != nil {
		t.Fatalf("PostMessage with no nudger wired: %v", err)
	}
	if messages.appendUserCalls != 1 {
		t.Errorf("AppendUserMessageAndEnqueueEvent called %d times, want 1 (the row must land regardless)",
			messages.appendUserCalls)
	}
}

// ---- Say: append-then-push (07 §3, §6) ------------------------------------

func TestTranscript_Say_AppendsThenPushes(t *testing.T) {
	messages := &fakeMessageStore{}
	sayer := &fakeSayPusher{}
	tx := runtime.NewTranscript(messages, sayer)

	if err := tx.Say(context.Background(), defaultTestProject, "hi there"); err != nil {
		t.Fatalf("Say: unexpected error: %v", err)
	}
	if messages.appendKilnCalls != 1 {
		t.Fatalf("AppendKilnMessage called %d times, want exactly 1", messages.appendKilnCalls)
	}
	pushed := sayer.pushedMessages()
	if len(pushed) != 1 {
		t.Fatalf("PushSay called %d times, want exactly 1", len(pushed))
	}
	if pushed[0].m.Text != "hi there" || pushed[0].m.Role != runtime.RoleKiln {
		t.Errorf("pushed message = %+v, want Text=%q Role=kiln", pushed[0].m, "hi there")
	}
	if pushed[0].projectID != defaultTestProject {
		t.Errorf("PushSay projectID = %q, want %q (the say fan-out is per-project, 11 §3)",
			pushed[0].projectID, defaultTestProject)
	}
}

// TestTranscript_Say_DoesNotPushWhenAppendFails proves the append-then-push
// ordering is real, not incidental: a failed append must never reach the
// SSE push (07 §3 — "a crash between them costs a live push, not history",
// implying the push only happens once the row is durable).
func TestTranscript_Say_DoesNotPushWhenAppendFails(t *testing.T) {
	messages := &fakeMessageStore{
		appendKilnFn: func(context.Context, string, string) (runtime.Message, error) {
			return runtime.Message{}, errStoreFailed
		},
	}
	sayer := &fakeSayPusher{}
	tx := runtime.NewTranscript(messages, sayer)

	if err := tx.Say(context.Background(), defaultTestProject, "hi"); !errors.Is(err, errStoreFailed) {
		t.Fatalf("Say error = %v, want errStoreFailed", err)
	}
	if got := len(sayer.pushedMessages()); got != 0 {
		t.Errorf("PushSay called %d times after a failed append, want 0", got)
	}
}

// ---- Recent (07 §3-§4) ------------------------------------------------------

func TestTranscript_Recent_DelegatesToMessageStore(t *testing.T) {
	ctx := context.Background()
	messages := &fakeMessageStore{}
	if _, _, err := messages.AppendUserMessageAndEnqueueEvent(ctx, defaultTestProject, "one"); err != nil {
		t.Fatal(err)
	}
	if _, err := messages.AppendKilnMessage(ctx, defaultTestProject, "two"); err != nil {
		t.Fatal(err)
	}
	tx := runtime.NewTranscript(messages, &fakeSayPusher{})

	got, err := tx.Recent(ctx, defaultTestProject, 20)
	if err != nil {
		t.Fatalf("Recent: unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Recent returned %d messages, want 2", len(got))
	}
	if got[0].Text != "one" || got[1].Text != "two" {
		t.Errorf("Recent order = [%q, %q], want oldest-first [one, two]", got[0].Text, got[1].Text)
	}
	if len(messages.recentCalls) != 1 || messages.recentCalls[0] != 20 {
		t.Errorf("MessageStore.Recent calls = %v, want a single call with n=20", messages.recentCalls)
	}
}
