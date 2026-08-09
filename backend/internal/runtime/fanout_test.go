package runtime_test

// FanOut unit tests: the push coordinator (04 §7, 08 §3–§4, §7) built over its
// four client-facing ports and the two units it fans out from — no store, no
// workers, no queue. The equivalent routing assertions made through Service in
// feed_test.go still pass and stay there until step 6 of the split moves the
// outbox routing they also exercise.
//
// What these add is the contract the routing tests could not reach: FanOut is
// best-effort. A failed assembly, a failed SSE push and a failed Web Push are
// each logged and dropped, and none of them stops the others — reaching those
// paths through the god object meant staging a failure across eleven fakes, a
// clock and a worker. HandleFeedCompletion is the one durable path here, and its
// failures are returned; that contrast is asserted rather than described.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/runtime"
)

var (
	errFeedPushFailed   = errors.New("fakeFeedPusher: synthetic failure")
	errBoardPushFailed  = errors.New("fakeSnapshotPusher: synthetic failure")
	errCompletionFailed = errors.New("fakeNotificationStore: synthetic failure")
)

// The two ephemeral activity kinds (08 §4), named once here rather than repeated
// as literals across the assertions below.
const (
	activityThinking = "thinking"
	activityToast    = "toast"
)

// proposalUpdate is one feed.updated descriptor carrying a pushable activity verb
// — the shared input for every "does this reach the notifier" assertion below,
// each of which varies something else (the mode, or which port fails).
const proposalUpdate = `{"title":"Login Redesign","verb":"proposal"}`

// fanoutRig is one FanOut wired over fresh fakes, each kept to hand so a test can
// stage a failure on it or read what it received. mode sets the owner's
// notification frequency, because the transition push FanOut builds goes through
// Notify's 02 §10 gate rather than straight to the notifier.
type fanoutRig struct {
	fanout    *runtime.FanOut
	snapshots *fakeSnapshotPusher
	feeds     *fakeFeedPusher
	activity  *fakeActivityPusher
	notes     *fakeNotificationStore
	board     *fakeBoardReader
	notifier  *fakeNotifier
}

func newFanOutRig(mode string) *fanoutRig {
	r := &fanoutRig{
		snapshots: &fakeSnapshotPusher{},
		feeds:     &fakeFeedPusher{},
		activity:  &fakeActivityPusher{},
		notes:     &fakeNotificationStore{},
		board:     &fakeBoardReader{},
		notifier:  &fakeNotifier{},
	}
	r.fanout = runtime.NewFanOut(
		r.snapshots, r.feeds, r.activity, r.notes,
		runtime.NewFeed(r.board, r.notes),
		runtime.NewNotify(r.notifier, &fakeOwner{mode: mode}),
	)
	return r
}

// feedUpdated builds one feed.updated outbox entry with the given change
// descriptor payload, as the board (or the runtime's own notification writes)
// would append it.
func feedUpdated(payload string) runtime.Entry {
	return runtime.Entry{ID: 1, ProjectID: defaultTestProject, Kind: "feed.updated", Payload: []byte(payload)}
}

// feedCompletion builds one feed.completion outbox entry, as the board's
// AcceptToDone would append it. The entry id doubles as the completion card's
// idempotency key (08 §7), so tests pass it explicitly.
func feedCompletion(id int64, payload string) runtime.Entry {
	return runtime.Entry{ID: id, ProjectID: defaultTestProject, Kind: "feed.completion", Payload: []byte(payload)}
}

// noteBody is the body of the one staged notification the assembly assertions
// recognize their card by.
const noteBody = "note"

// pushedPayload is the board.NotifyPayload shape a transition push carries
// (Title/Reason → Title/Body, plus the Kind the mode gate keys on), decoded back
// out of the notifier.
type pushedPayload struct {
	Title  string `json:"title"`
	Reason string `json:"reason"`
	Kind   string `json:"kind"`
}

func decodePush(t *testing.T, c recordedCall) pushedPayload {
	t.Helper()
	raw, ok := c.Args[1].([]byte)
	if !ok {
		t.Fatalf("Send payload arg type = %T, want []byte", c.Args[1])
	}
	var p pushedPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("decode push payload: %v", err)
	}
	return p
}

// ---- the thinking bracket (08 §4) -----------------------------------------

// The spinner brackets one brain pass: On=true going in, On=false coming out,
// both on the pass's own project (11 §3).
func TestFanOut_PushThinking_BracketsWithOnThenOff(t *testing.T) {
	r := newFanOutRig(modeDefault)

	r.fanout.PushThinking(context.Background(), defaultTestProject, true)
	r.fanout.PushThinking(context.Background(), defaultTestProject, false)

	evs := r.activity.events()
	if len(evs) != 2 {
		t.Fatalf("activity events = %d, want exactly 2 (on then off)", len(evs))
	}
	for i, p := range evs {
		if p.ev.Kind != activityThinking || p.ev.On == nil {
			t.Fatalf("event[%d] = %+v, want a thinking event with On set", i, p.ev)
		}
		if p.projectID != defaultTestProject {
			t.Errorf("event[%d] pushed to project %q, want %q (11 §3)", i, p.projectID, defaultTestProject)
		}
	}
	if *evs[0].ev.On != true || *evs[1].ev.On != false {
		t.Errorf("thinking sequence = [%v, %v], want [true, false]", *evs[0].ev.On, *evs[1].ev.On)
	}
}

// A lost spinner frame is cosmetic: the push failure is swallowed, so it can
// never derail the brain pass this brackets.
func TestFanOut_PushThinking_SwallowsAFailedPush(t *testing.T) {
	r := newFanOutRig(modeDefault)
	r.activity.pushErr = errFeedPushFailed

	r.fanout.PushThinking(context.Background(), defaultTestProject, true)

	if got := len(r.activity.events()); got != 1 {
		t.Fatalf("activity events = %d, want 1 — the push was attempted", got)
	}
}

// ---- board.updated (04 §7) -------------------------------------------------

// board.updated is the one route here that reports its failure: the dispatcher
// wraps it and the outbox retries, so the error must come back rather than be
// logged away.
func TestFanOut_PushBoard_DelegatesAndReturnsItsError(t *testing.T) {
	r := newFanOutRig(modeDefault)

	if err := r.fanout.PushBoard(context.Background(), defaultTestProject); err != nil {
		t.Fatalf("PushBoard: %v", err)
	}
	calls := r.snapshots.callsFor("PushBoard")
	if len(calls) != 1 {
		t.Fatalf("PushBoard calls = %d, want 1", len(calls))
	}
	if got, ok := calls[0].Args[0].(string); !ok || got != defaultTestProject {
		t.Errorf("PushBoard project = %v, want %q (11 §3)", calls[0].Args[0], defaultTestProject)
	}

	r.snapshots.pushBoardFn = func(context.Context, string) error { return errBoardPushFailed }
	if err := r.fanout.PushBoard(context.Background(), defaultTestProject); !errors.Is(err, errBoardPushFailed) {
		t.Errorf("PushBoard err = %v, want it to wrap %v", err, errBoardPushFailed)
	}
}

// ---- feed.updated (08 §3, §7) ----------------------------------------------

// The topic re-renders the whole feed: assemble the absolute snapshot through
// Feed, then fan it out to the entry's project.
func TestFanOut_HandleFeedUpdated_AssemblesAndPushesTheSnapshot(t *testing.T) {
	r := newFanOutRig(modeDefault)
	r.notes.seed(runtime.Notification{Kind: runtime.KindUpdate, Body: noteBody})

	r.fanout.HandleFeedUpdated(context.Background(), feedUpdated(`{}`))

	pushes := r.feeds.pushes()
	if len(pushes) != 1 {
		t.Fatalf("PushFeed calls = %d, want 1", len(pushes))
	}
	if len(pushes[0].snap.Cards) != 1 || pushes[0].snap.Cards[0].Body != noteBody {
		t.Errorf("pushed feed = %+v, want the assembled note card", pushes[0].snap)
	}
	if pushes[0].projectID != defaultTestProject {
		t.Errorf("PushFeed project = %q, want the entry's %q (11 §3)", pushes[0].projectID, defaultTestProject)
	}
}

// An assembly failure drops the whole re-render — there is no half-rendered
// snapshot to push and no transition to announce — and never surfaces to the
// outbox, because the next feed.updated re-renders from scratch.
func TestFanOut_HandleFeedUpdated_AssemblyFailureDropsTheFrame(t *testing.T) {
	r := newFanOutRig(modeAll)
	r.board.viewErr = errBoardViewFailed

	r.fanout.HandleFeedUpdated(context.Background(), feedUpdated(proposalUpdate))

	if got := len(r.feeds.pushes()); got != 0 {
		t.Errorf("PushFeed calls = %d, want 0 — there is no snapshot to push", got)
	}
	if got := r.notifier.count("Send"); got != 0 {
		t.Errorf("Send calls = %d, want 0 — an unassembled update announces nothing", got)
	}
}

// The SSE fan-out and the Web Push are independent best-effort effects: a client
// with no live connection must not cost the user the notification.
func TestFanOut_HandleFeedUpdated_PushFailureStillNotifies(t *testing.T) {
	r := newFanOutRig(modeAll)
	r.feeds.pushErr = errFeedPushFailed

	r.fanout.HandleFeedUpdated(context.Background(), feedUpdated(proposalUpdate))

	if got := r.notifier.count("Send"); got != 1 {
		t.Errorf("Send calls = %d, want 1 — a dropped SSE frame must not suppress the push", got)
	}
}

// The change verb decides the push copy, and the absence of one decides silence.
// Under "all" mode every activity verb names the ticket and what happened; the
// two milestone verbs stay silent because their dedicated notify.send already
// covers them, and an unknown verb or a title-less payload has nothing to say.
func TestFanOut_HandleFeedUpdated_VerbDrivesTheTransitionPush(t *testing.T) {
	cases := []struct {
		name, payload, wantBody string
	}{
		{"proposal", proposalUpdate, "New proposal"},
		{"reshaped", `{"title":"Login Redesign","verb":"reshaped"}`, "Proposal updated"},
		{"queued", `{"title":"Login Redesign","verb":"queued"}`, "Queued for work"},
		{"nudged", `{"title":"Login Redesign","verb":"nudged"}`, "Nudged"},
		{"archived", `{"title":"Login Redesign","verb":"archived"}`, "Archived"},
		{"milestone-blocked", `{"title":"Login Redesign","verb":"blocked"}`, ""},
		{"milestone-finished", `{"title":"Login Redesign","verb":"finished"}`, ""},
		{"unknown-verb", `{"title":"Login Redesign","verb":"wobbled"}`, ""},
		{"no-verb", `{"title":"Login Redesign"}`, ""},
		{"no-title", `{"verb":"proposal"}`, ""},
		{"empty-payload", `{}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newFanOutRig(modeAll)

			r.fanout.HandleFeedUpdated(context.Background(), feedUpdated(tc.payload))

			// Every case re-renders the feed; only the push differs.
			if got := len(r.feeds.pushes()); got != 1 {
				t.Fatalf("PushFeed calls = %d, want 1", got)
			}
			calls := r.notifier.callsFor("Send")
			if tc.wantBody == "" {
				if len(calls) != 0 {
					t.Fatalf("Send calls = %d, want 0 — %s carries no push copy", len(calls), tc.name)
				}
				return
			}
			if len(calls) != 1 {
				t.Fatalf("Send calls = %d, want exactly 1", len(calls))
			}
			got := decodePush(t, calls[0])
			if got.Title != "Login Redesign" || got.Reason != tc.wantBody {
				t.Errorf("push payload = %+v, want title=Login Redesign reason=%q", got, tc.wantBody)
			}
			// The push is tagged as feed-update activity, which is what the mode
			// gate keys on to admit it under "all" alone (02 §10).
			if got.Kind != kindUpdate {
				t.Errorf("push kind = %q, want %q", got.Kind, kindUpdate)
			}
		})
	}
}

// FanOut does not decide who hears a push — it hands every one to Notify.Send
// with the update kind, and the mode gate drops it under the recommended
// "default" mode. Same input as the "all" case above, opposite outcome.
func TestFanOut_HandleFeedUpdated_TransitionPushGoesThroughTheModeGate(t *testing.T) {
	r := newFanOutRig(modeDefault)

	r.fanout.HandleFeedUpdated(context.Background(), feedUpdated(proposalUpdate))

	if got := len(r.feeds.pushes()); got != 1 {
		t.Fatalf("PushFeed calls = %d, want 1 — the live re-render is not gated", got)
	}
	if got := r.notifier.count("Send"); got != 0 {
		t.Errorf("Send calls = %d, want 0 — feed activity is chatter under the default mode", got)
	}
}

// A malformed descriptor is a contract violation by the emitter, but the feed
// itself is still stale: re-render anyway and stay silent about the change,
// rather than wedging the outbox on a payload no retry will fix.
func TestFanOut_HandleFeedUpdated_MalformedPayloadStillRendersButStaysSilent(t *testing.T) {
	r := newFanOutRig(modeAll)

	r.fanout.HandleFeedUpdated(context.Background(), feedUpdated(`{"title":`))

	if got := len(r.feeds.pushes()); got != 1 {
		t.Errorf("PushFeed calls = %d, want 1 — the snapshot does not depend on the descriptor", got)
	}
	if got := r.notifier.count("Send"); got != 0 {
		t.Errorf("Send calls = %d, want 0 — an undecodable change describes nothing", got)
	}
}

// ---- activity.toast (08 §4, §7) --------------------------------------------

func TestFanOut_HandleActivityToast_DecodesAndPushes(t *testing.T) {
	r := newFanOutRig(modeDefault)
	e := runtime.Entry{
		ID: 3, ProjectID: defaultTestProject, Kind: "activity.toast",
		Payload: []byte(`{"verb":"started","ticket_id":"t-42","ticket_title":"Build the widget"}`),
	}

	r.fanout.HandleActivityToast(context.Background(), e)

	evs := r.activity.events()
	if len(evs) != 1 {
		t.Fatalf("activity events = %d, want 1", len(evs))
	}
	got := evs[0].ev
	if got.Kind != activityToast || got.Verb != "started" ||
		got.TicketID != "t-42" || got.TicketTitle != "Build the widget" {
		t.Errorf("toast = %+v, want Verb=started TicketID=t-42 TicketTitle='Build the widget'", got)
	}
	if evs[0].projectID != defaultTestProject {
		t.Errorf("toast pushed to project %q, want the entry's %q (11 §3)", evs[0].projectID, defaultTestProject)
	}
}

// The toast is ephemeral, so an undecodable one is dropped rather than retried —
// there is nothing to re-render and no reason to hold up the outbox.
func TestFanOut_HandleActivityToast_MalformedPayloadDropsWithoutPushing(t *testing.T) {
	r := newFanOutRig(modeDefault)
	e := runtime.Entry{ID: 3, ProjectID: defaultTestProject, Kind: "activity.toast", Payload: []byte(`{"verb":`)}

	r.fanout.HandleActivityToast(context.Background(), e)

	if got := len(r.activity.events()); got != 0 {
		t.Errorf("activity events = %d, want 0 — nothing decodable to push", got)
	}
}

// ---- feed.completion (08 §7) -----------------------------------------------

// The done card is the one durable thing FanOut writes: keyed on the outbox id
// so a redelivery is a no-op, bodyless like a poke, and carrying the link and
// summary of the landed work.
func TestFanOut_HandleFeedCompletion_PostsTheDoneCardKeyedOnTheOutboxID(t *testing.T) {
	r := newFanOutRig(modeDefault)
	e := feedCompletion(42, `{"ticket_id":"t1","ticket_title":"Build the widget",`+
		`"github_url":"https://github.com/o/r/pull/7","github_label":"#7",`+
		`"summary":"feat(web): build the widget"}`)

	if err := r.fanout.HandleFeedCompletion(context.Background(), e); err != nil {
		t.Fatalf("HandleFeedCompletion: %v", err)
	}

	posts := r.notes.completionPosts()
	if len(posts) != 1 {
		t.Fatalf("completion posts = %d, want 1", len(posts))
	}
	if posts[0].Key != e.ID {
		t.Errorf("completion key = %d, want the outbox id %d (idempotency, 08 §7)", posts[0].Key, e.ID)
	}
	if posts[0].TicketID != "t1" || posts[0].ProjectID != defaultTestProject {
		t.Errorf("completion = %+v, want ticket t1 on project %q", posts[0], defaultTestProject)
	}
	// Styled like a poke: the ticket title is the label and the client fronts a
	// ✅, so the card carries no prose of its own.
	if posts[0].Body != "" {
		t.Errorf("completion body = %q, want empty", posts[0].Body)
	}
	if posts[0].GitHubURL != "https://github.com/o/r/pull/7" || posts[0].GitHubLabel != "#7" {
		t.Errorf("completion github link = %q/%q, want the payload's pull-request URL + #7",
			posts[0].GitHubURL, posts[0].GitHubLabel)
	}
	if posts[0].WorkSummary != "feat(web): build the widget" {
		t.Errorf("completion work summary = %q, want the payload's summary", posts[0].WorkSummary)
	}

	// A redelivery of the same entry posts no second card.
	if err := r.fanout.HandleFeedCompletion(context.Background(), e); err != nil {
		t.Fatalf("HandleFeedCompletion redelivery: %v", err)
	}
	if got := len(r.notes.completionPosts()); got != 1 {
		t.Errorf("completion posts after redelivery = %d, want still 1", got)
	}
}

// Unlike its ephemeral siblings this card is persistent, so both failure modes —
// an undecodable payload and a failed write — come back as errors for the outbox
// to retry rather than being logged away.
func TestFanOut_HandleFeedCompletion_FailuresAreReturnedForRetry(t *testing.T) {
	t.Run("malformed payload", func(t *testing.T) {
		r := newFanOutRig(modeDefault)
		e := feedCompletion(42, `{`)

		if err := r.fanout.HandleFeedCompletion(context.Background(), e); err == nil {
			t.Error("HandleFeedCompletion err = nil, want a decode error — a durable card is not dropped silently")
		}
	})

	t.Run("store failure", func(t *testing.T) {
		r := newFanOutRig(modeDefault)
		r.notes.completionErr = errCompletionFailed
		e := feedCompletion(42, `{"ticket_id":"t1","ticket_title":"Build the widget"}`)

		err := r.fanout.HandleFeedCompletion(context.Background(), e)
		if !errors.Is(err, errCompletionFailed) {
			t.Errorf("HandleFeedCompletion err = %v, want it to wrap %v", err, errCompletionFailed)
		}
	})
}
