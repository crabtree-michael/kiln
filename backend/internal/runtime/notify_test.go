package runtime_test

// Notify unit tests: the tenant-scoped push choke point (02 §10, 11 §3) built
// over its two ports alone — no store, no workers, no queue. These are the
// extracted unit's own tests; the equivalent assertions made through Service in
// feed_test.go still pass, and stay there until the split's later steps move
// the routing they also exercise.

import (
	"context"
	"errors"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/runtime"
)

var (
	errOwnerLookupFailed = errors.New("fakeOwner: synthetic failure")
	errNotifierFailed    = errors.New("fakeNotifier: synthetic failure")
)

// The mode gate decides which kinds reach the user (02 §10): "blocked" delivers
// only the blocked milestone, "default" all three milestones but not the broad
// feed-update stream, "all" everything. An unrecognized mode is fail-safe — it
// behaves like "default" rather than falling silent.
func TestNotify_Send_ModeGate(t *testing.T) {
	cases := []struct {
		mode, kind string
		wantSend   int
	}{
		{modeDefault, kindBlocked, 1},
		{modeDefault, kindStarted, 1},
		{modeDefault, kindDone, 1},
		{modeDefault, kindUpdate, 0},
		{modeBlocked, kindBlocked, 1},
		{modeBlocked, kindStarted, 0},
		{modeBlocked, kindDone, 0},
		{modeBlocked, kindUpdate, 0},
		{modeAll, kindBlocked, 1},
		{modeAll, kindStarted, 1},
		{modeAll, kindDone, 1},
		{modeAll, kindUpdate, 1},
		{"nonsense", kindDone, 1},
		{"nonsense", kindUpdate, 0},
	}
	for _, tc := range cases {
		t.Run(tc.mode+"/"+tc.kind, func(t *testing.T) {
			notifier := &fakeNotifier{}
			n := runtime.NewNotify(notifier, &fakeOwner{mode: tc.mode})

			// A suppressed push is a successful no-op, not a delivery failure —
			// the outbox entry must still reach done.
			if err := n.Send(context.Background(), defaultTestProject, tc.kind, []byte(`{}`)); err != nil {
				t.Fatalf("Send: %v", err)
			}
			if got := notifier.count("Send"); got != tc.wantSend {
				t.Errorf("Send calls = %d, want %d for mode=%q kind=%q", got, tc.wantSend, tc.mode, tc.kind)
			}
		})
	}
}

// An admitted push resolves the project's owner first (the tenant guard) and
// hands the payload through byte-for-byte — the choke point routes, it does not
// rewrite.
func TestNotify_Send_ResolvesOwnerThenForwardsPayload(t *testing.T) {
	owner := &fakeOwner{}
	notifier := &fakeNotifier{}
	n := runtime.NewNotify(notifier, owner)

	payload := []byte(`{"title":"Login Redesign","reason":"needs a decision","kind":"blocked"}`)
	if err := n.Send(context.Background(), defaultTestProject, kindBlocked, payload); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if got := owner.count("Owner"); got != 1 {
		t.Errorf("Owner calls = %d, want 1 — every send resolves the owning user (11 §3)", got)
	}
	calls := notifier.callsFor("Send")
	if len(calls) != 1 {
		t.Fatalf("Send calls = %d, want exactly 1", len(calls))
	}
	if got, ok := calls[0].Args[0].(string); !ok || got != defaultTestProject {
		t.Errorf("Send project = %v, want %q", calls[0].Args[0], defaultTestProject)
	}
	got, ok := calls[0].Args[1].([]byte)
	if !ok {
		t.Fatalf("Send payload arg type = %T, want []byte", calls[0].Args[1])
	}
	if string(got) != string(payload) {
		t.Errorf("Send payload = %s, want %s", got, payload)
	}
}

// An unowned or unresolvable project must not emit a push at all: the failure is
// returned so the notify.send retry/dead-letter machinery treats it like any
// other delivery failure, and the notifier is never reached.
func TestNotify_Send_OwnerResolutionFailureIsReturnedAndSuppressesSend(t *testing.T) {
	notifier := &fakeNotifier{}
	owner := &fakeOwner{ownerFn: func(context.Context, string) (string, error) {
		return "", errOwnerLookupFailed
	}}
	n := runtime.NewNotify(notifier, owner)

	err := n.Send(context.Background(), defaultTestProject, kindBlocked, []byte(`{}`))
	if !errors.Is(err, errOwnerLookupFailed) {
		t.Fatalf("Send err = %v, want it to wrap %v", err, errOwnerLookupFailed)
	}
	if got := notifier.count("Send"); got != 0 {
		t.Errorf("Send calls = %d, want 0 — an unresolvable owner must not be pushed to", got)
	}
}

// A mode-resolution failure is the same class of problem: retryable, and no push
// goes out on a guess.
func TestNotify_Send_ModeResolutionFailureIsReturnedAndSuppressesSend(t *testing.T) {
	notifier := &fakeNotifier{}
	owner := &fakeOwner{modeFn: func(context.Context, string) (string, error) {
		return "", errOwnerLookupFailed
	}}
	n := runtime.NewNotify(notifier, owner)

	err := n.Send(context.Background(), defaultTestProject, kindBlocked, []byte(`{}`))
	if !errors.Is(err, errOwnerLookupFailed) {
		t.Fatalf("Send err = %v, want it to wrap %v", err, errOwnerLookupFailed)
	}
	if got := notifier.count("Send"); got != 0 {
		t.Errorf("Send calls = %d, want 0 — an unresolvable mode must not be pushed through", got)
	}
}

// With no Owner wired (single-user boot, identity off) there is no tenant to
// check and no per-user mode to read: the guard is skipped and the mode falls
// back to "default" — the milestones, never silence.
func TestNotify_Send_NilOwnerFallsBackToDefaultMode(t *testing.T) {
	notifier := &fakeNotifier{}
	n := runtime.NewNotify(notifier, nil)

	if err := n.Send(context.Background(), defaultTestProject, kindDone, []byte(`{}`)); err != nil {
		t.Fatalf("Send milestone: %v", err)
	}
	if got := notifier.count("Send"); got != 1 {
		t.Fatalf("Send calls = %d, want 1 — a milestone still delivers without an Owner", got)
	}

	if err := n.Send(context.Background(), defaultTestProject, kindUpdate, []byte(`{}`)); err != nil {
		t.Fatalf("Send update: %v", err)
	}
	if got := notifier.count("Send"); got != 1 {
		t.Errorf("Send calls = %d, want still 1 — the default mode drops feed-update chatter", got)
	}
}

// A real delivery failure — the push was admitted but the notifier could not
// send it — is returned so the outbox retries.
func TestNotify_Send_NotifierFailureIsReturned(t *testing.T) {
	notifier := &fakeNotifier{sendFn: func(context.Context, string, []byte) error { return errNotifierFailed }}
	n := runtime.NewNotify(notifier, &fakeOwner{})

	err := n.Send(context.Background(), defaultTestProject, kindBlocked, []byte(`{}`))
	if !errors.Is(err, errNotifierFailed) {
		t.Fatalf("Send err = %v, want it to wrap %v", err, errNotifierFailed)
	}
}
