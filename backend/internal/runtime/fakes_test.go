package runtime_test

// Shared unit-test fakes for the runtime module (04 §9: "the drain loop
// against fake handlers... the worker takes a clock interface so backoff is
// tested without sleeping"). Every fake here is in-memory and offline;
// nothing in this file talks to a network, a real clock tick, or Postgres —
// that is postgres/store_integration_test.go's job.

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/runtime"
	"github.com/crabtree-michael/kiln/backend/internal/testutil"
)

// statusDone is the terminal delivery status a successful entry lands in
// (04 §3), shared across the runtime unit tests.
const statusDone = "done"

// defaultTestProject is the tenant every single-project test seeds under
// (11 §3) — unit fakes don't require uuid syntax, only equality.
const defaultTestProject = "proj-default"

// pumpStep is the simulated time each pump heartbeat advances — the worker's
// 1s poll fallback (04 §5), so one real millisecond crosses one simulated
// poll cycle.
const pumpStep = time.Second

// realTestClock is the trivial wall-clock runtime.Clock, used only by the
// nudge-vs-poll test (worker_test.go), which deliberately measures real
// elapsed time to prove Nudge beats the 1s poll fallback.
type realTestClock struct{}

func (realTestClock) Now() time.Time                         { return time.Now() }
func (realTestClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

var _ runtime.Clock = realTestClock{}

// runWorker starts w.Run in the background and returns a stop func that
// cancels the context and waits (bounded) for Run to return.
func runWorker(t *testing.T, w *runtime.Worker) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	return func() {
		cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("Worker.Run returned an unexpected error after cancellation: %v", err)
			}
		case <-time.After(testutil.EventuallyTimeout):
			t.Error("Worker.Run did not return after context cancellation")
		}
	}
}

// ---- fakeStore ------------------------------------------------------------

// queueRow is one in-memory row of either queue, mirroring the shared
// delivery-state columns (04 §2) plus the tenant column (11 §3).
type queueRow struct {
	id            int64
	projectID     string
	kind          string
	payload       []byte
	createdAt     time.Time
	status        string // "pending", "done", "dead"
	attempts      int
	nextAttemptAt time.Time
	lastError     string
}

// claimCall records one ClaimNextDue invocation — the busy set the dispatcher
// passed is what the per-project serialization tests assert on (11 §3).
type claimCall struct {
	queue runtime.QueueName
	busy  []string
}

type retryCall struct {
	queue         runtime.QueueName
	id            int64
	lastError     string
	nextAttemptAt time.Time
	calledAt      time.Time
}

type deadCall struct {
	queue     runtime.QueueName
	id        int64
	lastError string
	calledAt  time.Time
}

// fakeStore is an in-memory runtime.Store over both queues, driven by a
// shared Clock so "due" (next_attempt_at <= now) means exactly what it means
// against real Postgres (04 §3 step 1), and so the backoff schedule can be
// asserted against the same fake clock the worker itself uses.
type fakeStore struct {
	mu     sync.Mutex
	clock  runtime.Clock
	rows   map[runtime.QueueName]map[int64]*queueRow
	nextID map[runtime.QueueName]int64

	doneCalls []struct {
		queue runtime.QueueName
		id    int64
	}
	retryCalls []retryCall
	deadCalls  []deadCall
	claimCalls []claimCall
	seenKeys   map[int64]struct{} // non-zero event idempotency keys already admitted
}

func newFakeStore(clock runtime.Clock) *fakeStore {
	return &fakeStore{
		clock: clock,
		rows: map[runtime.QueueName]map[int64]*queueRow{
			runtime.QueueEvents: {},
			runtime.QueueOutbox: {},
		},
		nextID: map[runtime.QueueName]int64{},
	}
}

func (s *fakeStore) InsertEvent(
	_ context.Context, projectID string, t runtime.EventType, idempotencyKey int64, payload []byte,
) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Model the events-queue partial unique index (architecture audit 3.1): a
	// non-zero key already admitted is a no-op returning id 0.
	if idempotencyKey != 0 {
		if _, ok := s.seenKeys[idempotencyKey]; ok {
			return 0, nil
		}
		if s.seenKeys == nil {
			s.seenKeys = map[int64]struct{}{}
		}
		s.seenKeys[idempotencyKey] = struct{}{}
	}
	return s.insertLocked(runtime.QueueEvents, projectID, string(t), payload, 0, "pending"), nil
}

func (s *fakeStore) ClaimNextDue(
	_ context.Context, q runtime.QueueName, busy []string,
) (runtime.Entry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claimCalls = append(s.claimCalls, claimCall{queue: q, busy: append([]string(nil), busy...)})
	now := s.clock.Now()

	var ids []int64
	for id, row := range s.rows[q] {
		if row.status == "pending" && !row.nextAttemptAt.After(now) && !slices.Contains(busy, row.projectID) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return runtime.Entry{}, false, nil
	}
	slices.Sort(ids)
	row := s.rows[q][ids[0]]
	row.attempts++
	return runtime.Entry{
		ID: row.id, ProjectID: row.projectID, Kind: row.kind, Payload: append([]byte(nil), row.payload...),
		Attempts: row.attempts, CreatedAt: row.createdAt,
	}, true, nil
}

func (s *fakeStore) MarkDone(_ context.Context, q runtime.QueueName, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[q][id].status = statusDone
	s.doneCalls = append(s.doneCalls, struct {
		queue runtime.QueueName
		id    int64
	}{q, id})
	return nil
}

func (s *fakeStore) MarkRetry(
	_ context.Context, q runtime.QueueName, id int64, lastError string, nextAttemptAt time.Time,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.rows[q][id]
	row.lastError = lastError
	row.nextAttemptAt = nextAttemptAt
	s.retryCalls = append(s.retryCalls, retryCall{
		queue: q, id: id, lastError: lastError, nextAttemptAt: nextAttemptAt, calledAt: s.clock.Now(),
	})
	return nil
}

func (s *fakeStore) MarkDead(_ context.Context, q runtime.QueueName, id int64, lastError string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.rows[q][id]
	row.status = "dead"
	row.lastError = lastError
	s.deadCalls = append(s.deadCalls, deadCall{queue: q, id: id, lastError: lastError, calledAt: s.clock.Now()})
	return nil
}

// seed inserts a pending row directly into q, bypassing InsertEvent — the
// outbox is never written through this port (the board appends it
// transactionally, 04 §2), and pre-attempted rows model a crash between claim
// and mark (04 §5) without needing a real crash. The row belongs to
// defaultTestProject; seedProject stages multi-tenant scenarios.
func (s *fakeStore) seed(q runtime.QueueName, kind string, payload []byte, attempts int) int64 {
	return s.seedProject(q, defaultTestProject, kind, payload, attempts)
}

// seedProject is seed with an explicit tenant (11 §3), for the dispatcher's
// per-project serialization tests.
func (s *fakeStore) seedProject(q runtime.QueueName, projectID, kind string, payload []byte, attempts int) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.insertLocked(q, projectID, kind, payload, attempts, "pending")
}

func (s *fakeStore) insertLocked(
	q runtime.QueueName, projectID, kind string, payload []byte, attempts int, status string,
) int64 {
	s.nextID[q]++
	id := s.nextID[q]
	s.rows[q][id] = &queueRow{
		id: id, projectID: projectID, kind: kind, payload: append([]byte(nil), payload...),
		createdAt: s.clock.Now(), status: status, attempts: attempts,
		nextAttemptAt: s.clock.Now(),
	}
	return id
}

func (s *fakeStore) claimBusyLists(q runtime.QueueName) [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out [][]string
	for _, c := range s.claimCalls {
		if c.queue == q {
			out = append(out, c.busy)
		}
	}
	return out
}

func (s *fakeStore) status(q runtime.QueueName, id int64) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rows[q][id].status
}

// attempts reads an events-queue row's claim count (the only queue the
// attempt-count assertions inspect).
func (s *fakeStore) attempts(id int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rows[runtime.QueueEvents][id].attempts
}

func (s *fakeStore) retryCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.retryCalls)
}

func (s *fakeStore) retryCallsFor(id int64) []retryCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []retryCall
	for _, c := range s.retryCalls {
		if c.id == id {
			out = append(out, c)
		}
	}
	return out
}

func (s *fakeStore) deadCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.deadCalls)
}

func (s *fakeStore) deadCallsFor(id int64) []deadCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []deadCall
	for _, c := range s.deadCalls {
		if c.id == id {
			out = append(out, c)
		}
	}
	return out
}

func (s *fakeStore) doneCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.doneCalls)
}

var _ runtime.Store = (*fakeStore)(nil)

// ---- fakeBrain, fakePuller, fakeBlocker, fakeAgentRuntime, fakeNotifier,
// fakeSnapshotPusher: the executor ports service.go names (04 §2). Every
// method records its call before consulting an optional override func —
// mirroring brain_test.fakeBoard's pattern — so tests configure only the
// scenario they care about (e.g. always-erroring Send for the exhaustion/
// dead-letter tests).

type recordedCall struct {
	Method string
	Args   []any
}

type callRecorder struct {
	mu    sync.Mutex
	calls []recordedCall
}

func (r *callRecorder) record(method string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, recordedCall{Method: method, Args: args})
}

func (r *callRecorder) count(method string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.calls {
		if c.Method == method {
			n++
		}
	}
	return n
}

func (r *callRecorder) callsFor(method string) []recordedCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []recordedCall
	for _, c := range r.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

type fakeBrain struct {
	callRecorder

	handleEventFn func(ctx context.Context, ev runtime.Event) error
}

func (f *fakeBrain) HandleEvent(ctx context.Context, ev runtime.Event) error {
	f.record("HandleEvent", ev)
	if f.handleEventFn != nil {
		return f.handleEventFn(ctx, ev)
	}
	return nil
}

var _ runtime.Brain = (*fakeBrain)(nil)

// fakeBrainResolver is the per-project brain resolution port (11 §3). By
// default every project resolves to the one wrapped fakeBrain; forFn overrides
// for resolution-failure scenarios.
type fakeBrainResolver struct {
	callRecorder

	brain runtime.Brain
	forFn func(ctx context.Context, projectID string) (runtime.Brain, error)
}

func (f *fakeBrainResolver) For(ctx context.Context, projectID string) (runtime.Brain, error) {
	f.record("For", projectID)
	if f.forFn != nil {
		return f.forFn(ctx, projectID)
	}
	return f.brain, nil
}

// resolverFor wraps a fakeBrain as an always-succeeding resolver — the
// single-tenant default most tests want.
func resolverFor(b *fakeBrain) *fakeBrainResolver { return &fakeBrainResolver{brain: b} }

var _ runtime.BrainResolver = (*fakeBrainResolver)(nil)

// fakeOwner resolves every project to one test user unless ownerFn overrides.
type fakeOwner struct {
	callRecorder

	ownerFn func(ctx context.Context, projectID string) (string, error)
}

func (f *fakeOwner) Owner(ctx context.Context, projectID string) (string, error) {
	f.record("Owner", projectID)
	if f.ownerFn != nil {
		return f.ownerFn(ctx, projectID)
	}
	return "user-owner", nil
}

var _ runtime.Owner = (*fakeOwner)(nil)

type fakePuller struct {
	callRecorder

	runPullFn func(ctx context.Context, projectID string) error
}

func (f *fakePuller) RunPull(ctx context.Context, projectID string) error {
	f.record("RunPull", projectID)
	if f.runPullFn != nil {
		return f.runPullFn(ctx, projectID)
	}
	return nil
}

var _ runtime.Puller = (*fakePuller)(nil)

type fakeBlocker struct {
	callRecorder

	markBlockedFn func(ctx context.Context, projectID, ticketID, reason string) error
}

func (f *fakeBlocker) MarkBlocked(ctx context.Context, projectID, ticketID, reason string) error {
	f.record("MarkBlocked", projectID, ticketID, reason)
	if f.markBlockedFn != nil {
		return f.markBlockedFn(ctx, projectID, ticketID, reason)
	}
	return nil
}

var _ runtime.Blocker = (*fakeBlocker)(nil)

type fakeAgentRuntime struct {
	callRecorder

	sendFn    func(ctx context.Context, projectID string, idempotencyKey int64, payload []byte) error
	releaseFn func(ctx context.Context, projectID string, idempotencyKey int64, payload []byte) error
}

func (f *fakeAgentRuntime) Send(ctx context.Context, projectID string, idempotencyKey int64, payload []byte) error {
	f.record("Send", projectID, idempotencyKey, append([]byte(nil), payload...))
	if f.sendFn != nil {
		return f.sendFn(ctx, projectID, idempotencyKey, payload)
	}
	return nil
}

func (f *fakeAgentRuntime) Release(ctx context.Context, projectID string, idempotencyKey int64, payload []byte) error {
	f.record("Release", projectID, idempotencyKey, append([]byte(nil), payload...))
	if f.releaseFn != nil {
		return f.releaseFn(ctx, projectID, idempotencyKey, payload)
	}
	return nil
}

var _ runtime.AgentRuntime = (*fakeAgentRuntime)(nil)

type fakeNotifier struct {
	callRecorder

	sendFn func(ctx context.Context, projectID string, payload []byte) error
}

func (f *fakeNotifier) Send(ctx context.Context, projectID string, payload []byte) error {
	f.record("Send", projectID, append([]byte(nil), payload...))
	if f.sendFn != nil {
		return f.sendFn(ctx, projectID, payload)
	}
	return nil
}

var _ runtime.Notifier = (*fakeNotifier)(nil)

type fakeSnapshotPusher struct {
	callRecorder

	pushBoardFn func(ctx context.Context, projectID string) error
}

func (f *fakeSnapshotPusher) PushBoard(ctx context.Context, projectID string) error {
	f.record("PushBoard", projectID)
	if f.pushBoardFn != nil {
		return f.pushBoardFn(ctx, projectID)
	}
	return nil
}

var _ runtime.SnapshotPusher = (*fakeSnapshotPusher)(nil)

// ---- fakeMessageStore, fakeSayPusher: 07 §3's transcript ports -----------

type fakeMessageStore struct {
	mu       sync.Mutex
	messages []runtime.Message
	nextID   int64
	nextEvID int64

	appendUserFn func(ctx context.Context, projectID, text string) (int64, int64, error)
	appendKilnFn func(ctx context.Context, projectID, text string) (runtime.Message, error)

	appendUserCalls    int
	appendKilnCalls    int
	appendKilnProjects []string
	recentCalls        []int
	recentProjects     []string
}

func (f *fakeMessageStore) AppendUserMessageAndEnqueueEvent(
	ctx context.Context, projectID, text string,
) (int64, int64, error) {
	f.mu.Lock()
	f.appendUserCalls++
	f.mu.Unlock()
	if f.appendUserFn != nil {
		return f.appendUserFn(ctx, projectID, text)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	f.nextEvID++
	f.messages = append(f.messages, runtime.Message{
		ID: f.nextID, Role: runtime.RoleUser, Text: text, CreatedAt: time.Now(),
	})
	return f.nextID, f.nextEvID, nil
}

func (f *fakeMessageStore) AppendKilnMessage(ctx context.Context, projectID, text string) (runtime.Message, error) {
	f.mu.Lock()
	f.appendKilnCalls++
	f.appendKilnProjects = append(f.appendKilnProjects, projectID)
	f.mu.Unlock()
	if f.appendKilnFn != nil {
		return f.appendKilnFn(ctx, projectID, text)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	m := runtime.Message{ID: f.nextID, Role: runtime.RoleKiln, Text: text, CreatedAt: time.Now()}
	f.messages = append(f.messages, m)
	return m, nil
}

func (f *fakeMessageStore) Recent(_ context.Context, projectID string, n int) ([]runtime.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recentCalls = append(f.recentCalls, n)
	f.recentProjects = append(f.recentProjects, projectID)
	if n >= len(f.messages) {
		out := make([]runtime.Message, len(f.messages))
		copy(out, f.messages)
		return out, nil
	}
	out := make([]runtime.Message, n)
	copy(out, f.messages[len(f.messages)-n:])
	return out, nil
}

var _ runtime.MessageStore = (*fakeMessageStore)(nil)

type sayPush struct {
	projectID string
	m         runtime.Message
}

type fakeSayPusher struct {
	mu        sync.Mutex
	pushed    []sayPush
	pushSayFn func(ctx context.Context, projectID string, m runtime.Message) error
}

func (f *fakeSayPusher) PushSay(ctx context.Context, projectID string, m runtime.Message) error {
	f.mu.Lock()
	f.pushed = append(f.pushed, sayPush{projectID: projectID, m: m})
	f.mu.Unlock()
	if f.pushSayFn != nil {
		return f.pushSayFn(ctx, projectID, m)
	}
	return nil
}

func (f *fakeSayPusher) pushedMessages() []sayPush {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sayPush, len(f.pushed))
	copy(out, f.pushed)
	return out
}

var _ runtime.SayPusher = (*fakeSayPusher)(nil)

// ---- fakeNotificationStore, fakeBoardReader, fakeFeedPusher,
// fakeActivityPusher: the 08 §7 ports service.go names. In-memory and offline,
// mirroring the other fakes; the postgres NotificationStore is exercised in
// store_integration_test.go.

// fakeNotificationStore is an in-memory runtime.NotificationStore: post/retract/
// mark-seen mutate an in-memory slice, and UnseenNotifications returns the
// neither-seen-nor-retracted rows newest-first (08 §3).
type fakeNotificationStore struct {
	mu    sync.Mutex
	rows  []runtime.Notification
	next  int64
	posts []runtime.Notification

	postFn func(
		ctx context.Context, projectID, kind, body string, ticketID, imageURL *string,
	) (runtime.Notification, error)
	retractFn      func(ctx context.Context, projectID string, id int64) error
	postProjects   []string
	markSeenN      []int64
	edits          []notificationEdit
	completionKeys map[int64]bool   // idempotency keys already posted
	completions    []completionPost // one per accepted (non-duplicate) completion card
}

// completionPost records one accepted PostCompletionCard call.
type completionPost struct {
	ProjectID   string
	Key         int64
	TicketID    string
	Body        string
	GitHubURL   string
	GitHubLabel string
	WorkSummary string
}

// notificationEdit records one EditNotification call for delegation assertions.
type notificationEdit struct {
	ID       int64
	Kind     string
	Body     string
	ImageURL *string
}

func (f *fakeNotificationStore) PostNotification(
	ctx context.Context, projectID, kind, body string, ticketID, imageURL *string,
) (runtime.Notification, error) {
	if f.postFn != nil {
		return f.postFn(ctx, projectID, kind, body, ticketID, imageURL)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	n := runtime.Notification{
		ID: f.next, Kind: runtime.NotificationKind(kind), Body: body,
		TicketID: ticketID, ImageURL: imageURL, CreatedAt: time.Now(),
	}
	f.rows = append(f.rows, n)
	f.posts = append(f.posts, n)
	f.postProjects = append(f.postProjects, projectID)
	return n, nil
}

func (f *fakeNotificationStore) PostCompletionCard(
	_ context.Context, projectID string, key int64, ticketID, body, githubURL, githubLabel, workSummary string,
) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.completionKeys == nil {
		f.completionKeys = map[int64]bool{}
	}
	if f.completionKeys[key] {
		return false, nil // duplicate delivery: no-op
	}
	f.completionKeys[key] = true
	f.next++
	n := runtime.Notification{
		ID: f.next, Kind: runtime.KindDone, Body: body, TicketID: &ticketID, CreatedAt: time.Now(),
	}
	if githubURL != "" {
		n.GitHubURL = &githubURL
	}
	if githubLabel != "" {
		n.GitHubLabel = &githubLabel
	}
	if workSummary != "" {
		n.WorkSummary = &workSummary
	}
	f.rows = append(f.rows, n)
	f.completions = append(f.completions, completionPost{
		ProjectID: projectID, Key: key, TicketID: ticketID, Body: body,
		GitHubURL: githubURL, GitHubLabel: githubLabel, WorkSummary: workSummary,
	})
	return true, nil
}

func (f *fakeNotificationStore) RetractNotification(ctx context.Context, projectID string, id int64) error {
	if f.retractFn != nil {
		return f.retractFn(ctx, projectID, id)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	for i := range f.rows {
		if f.rows[i].ID == id {
			f.rows[i].RetractedAt = &now
		}
	}
	return nil
}

func (f *fakeNotificationStore) RetractAllNotifications(_ context.Context, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	for i := range f.rows {
		if f.rows[i].RetractedAt == nil {
			f.rows[i].RetractedAt = &now
		}
	}
	return nil
}

func (f *fakeNotificationStore) EditNotification(
	_ context.Context, _ string, id int64, kind, body string, imageURL *string,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.edits = append(f.edits, notificationEdit{ID: id, Kind: kind, Body: body, ImageURL: imageURL})
	for i := range f.rows {
		if f.rows[i].ID == id && f.rows[i].RetractedAt == nil && f.rows[i].SeenAt == nil {
			f.rows[i].Kind = runtime.NotificationKind(kind)
			f.rows[i].Body = body
			f.rows[i].ImageURL = imageURL
		}
	}
	return nil
}

func (f *fakeNotificationStore) MarkSeen(_ context.Context, _ string, lastID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.markSeenN = append(f.markSeenN, lastID)
	now := time.Now()
	for i := range f.rows {
		if f.rows[i].SeenAt == nil && f.rows[i].ID <= lastID {
			f.rows[i].SeenAt = &now
		}
	}
	return nil
}

func (f *fakeNotificationStore) UnseenNotifications(_ context.Context, _ string) ([]runtime.Notification, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []runtime.Notification
	for _, n := range slices.Backward(f.rows) { // newest-first
		if n.SeenAt == nil && n.RetractedAt == nil {
			out = append(out, n)
		}
	}
	return out, nil
}

func (f *fakeNotificationStore) RecentNotifications(
	_ context.Context, _ string, limit int,
) ([]runtime.Notification, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []runtime.Notification
	for _, n := range slices.Backward(f.rows) { // newest-first, seen AND unseen
		if n.RetractedAt == nil {
			out = append(out, n)
		}
	}
	if len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

func (f *fakeNotificationStore) HistoryBefore(
	_ context.Context, _ string, before int64, limit int,
) ([]runtime.Notification, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []runtime.Notification
	for _, n := range slices.Backward(f.rows) { // newest-first
		if n.RetractedAt == nil && n.ID < before {
			out = append(out, n)
		}
	}
	if len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

func (f *fakeNotificationStore) LastSeenID(_ context.Context, _ string) (*int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var maxID *int64
	for i := range f.rows {
		if f.rows[i].SeenAt != nil && (maxID == nil || f.rows[i].ID > *maxID) {
			id := f.rows[i].ID
			maxID = &id
		}
	}
	return maxID, nil
}

func (f *fakeNotificationStore) UnseenCount(_ context.Context, _ string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for i := range f.rows {
		if f.rows[i].SeenAt == nil && f.rows[i].RetractedAt == nil {
			n++
		}
	}
	return n, nil
}

// seed inserts an already-persisted notification directly, bypassing the tx
// path, so feed-assembly tests can stage fixed rows.
func (f *fakeNotificationStore) seed(n runtime.Notification) runtime.Notification {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	n.ID = f.next
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now()
	}
	f.rows = append(f.rows, n)
	return n
}

func (f *fakeNotificationStore) completionPosts() []completionPost {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]completionPost(nil), f.completions...)
}

var _ runtime.NotificationStore = (*fakeNotificationStore)(nil)

// fakeBoardReader is an in-memory runtime.BoardReader returning a staged
// BoardView, recording the projects it was asked for.
type fakeBoardReader struct {
	mu       sync.Mutex
	view     runtime.BoardView
	viewErr  error
	projects []string
}

func (f *fakeBoardReader) BoardView(_ context.Context, projectID string) (runtime.BoardView, error) {
	f.mu.Lock()
	f.projects = append(f.projects, projectID)
	f.mu.Unlock()
	return f.view, f.viewErr
}

var _ runtime.BoardReader = (*fakeBoardReader)(nil)

// feedPush records one PushFeed call: the target project and its snapshot.
type feedPush struct {
	projectID string
	snap      runtime.FeedSnapshot
}

// fakeFeedPusher records every pushed FeedSnapshot (08 §3).
type fakeFeedPusher struct {
	mu     sync.Mutex
	pushed []feedPush
}

func (f *fakeFeedPusher) PushFeed(_ context.Context, projectID string, snap runtime.FeedSnapshot) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pushed = append(f.pushed, feedPush{projectID: projectID, snap: snap})
	return nil
}

func (f *fakeFeedPusher) pushes() []feedPush {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]feedPush, len(f.pushed))
	copy(out, f.pushed)
	return out
}

var _ runtime.FeedPusher = (*fakeFeedPusher)(nil)

// activityPush records one PushActivity call: the target project and event.
type activityPush struct {
	projectID string
	ev        runtime.ActivityEvent
}

// fakeActivityPusher records every pushed ActivityEvent (08 §4).
type fakeActivityPusher struct {
	mu     sync.Mutex
	pushed []activityPush
}

func (f *fakeActivityPusher) PushActivity(_ context.Context, projectID string, ev runtime.ActivityEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pushed = append(f.pushed, activityPush{projectID: projectID, ev: ev})
	return nil
}

func (f *fakeActivityPusher) events() []activityPush {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]activityPush, len(f.pushed))
	copy(out, f.pushed)
	return out
}

var _ runtime.ActivityPusher = (*fakeActivityPusher)(nil)
