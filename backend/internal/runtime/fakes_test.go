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
)

// statusDone is the terminal delivery status a successful entry lands in
// (04 §3), shared across the runtime unit tests.
const statusDone = "done"

// ---- fakeClock -----------------------------------------------------------

// clockWaiter is one pending After() call: fired once the fake clock's Now()
// reaches or passes deadline.
type clockWaiter struct {
	deadline time.Time
	ch       chan time.Time
}

// fakeClock is a manually-advanced runtime.Clock. Tests step simulated time
// forward (via Advance/pump) so the worker's backoff schedule (04 §3, D8:
// min(1s*2^(attempts-1), 60s), up to 8 attempts — nearly two minutes of
// simulated waiting) never costs real wall time (04 §9).
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []clockWaiter
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	c.waiters = append(c.waiters, clockWaiter{deadline: c.now.Add(d), ch: ch})
	return ch
}

// Advance moves the clock forward by d and fires every waiter whose deadline
// has elapsed.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	remaining := c.waiters[:0]
	for _, w := range c.waiters {
		if !w.deadline.After(c.now) {
			w.ch <- c.now
		} else {
			remaining = append(remaining, w)
		}
	}
	c.waiters = remaining
}

// pumpStep is the simulated time each pump heartbeat advances — the worker's
// 1s poll fallback (04 §5), so one real millisecond crosses one simulated
// poll cycle.
const pumpStep = time.Second

// pump advances the clock by pumpStep on a tight real-time heartbeat until stop
// is closed — the "without real sleeps" trick (04 §9): the *simulated*
// interval the worker waits on (its 1s poll fallback, 04 §5) is what
// advances; the real wall-clock cost is only the heartbeat tick, so a test
// crosses many simulated poll cycles — and the whole 8-attempt, ~183s
// backoff ladder — in well under a second of real time.
func (c *fakeClock) pump(stop <-chan struct{}) {
	t := time.NewTicker(time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			c.Advance(pumpStep)
		}
	}
}

// realTestClock is the trivial wall-clock runtime.Clock, used only by the
// nudge-vs-poll test (worker_test.go), which deliberately measures real
// elapsed time to prove Nudge beats the 1s poll fallback.
type realTestClock struct{}

func (realTestClock) Now() time.Time                         { return time.Now() }
func (realTestClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

var _ runtime.Clock = realTestClock{}

// ---- eventually -----------------------------------------------------------

// eventuallyTimeout bounds every eventually() wait below: real scheduling
// slack only, never the worker's own poll/backoff intervals — those are
// owned and sped up by fakeClock.
const eventuallyTimeout = 2 * time.Second

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(eventuallyTimeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !cond() {
		t.Fatalf("condition not met within %s", eventuallyTimeout)
	}
}

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
		case <-time.After(eventuallyTimeout):
			t.Error("Worker.Run did not return after context cancellation")
		}
	}
}

// ---- fakeStore ------------------------------------------------------------

// queueRow is one in-memory row of either queue, mirroring the shared
// delivery-state columns (04 §2).
type queueRow struct {
	id            int64
	kind          string
	payload       []byte
	createdAt     time.Time
	status        string // "pending", "done", "dead"
	attempts      int
	nextAttemptAt time.Time
	lastError     string
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

func (s *fakeStore) InsertEvent(_ context.Context, t runtime.EventType, payload []byte) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.insertLocked(runtime.QueueEvents, string(t), payload, 0, "pending"), nil
}

func (s *fakeStore) ClaimNextDue(_ context.Context, q runtime.QueueName) (runtime.Entry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()

	var ids []int64
	for id, row := range s.rows[q] {
		if row.status == "pending" && !row.nextAttemptAt.After(now) {
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
		ID: row.id, Kind: row.kind, Payload: append([]byte(nil), row.payload...),
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
// and mark (04 §5) without needing a real crash.
func (s *fakeStore) seed(q runtime.QueueName, kind string, payload []byte, attempts int) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.insertLocked(q, kind, payload, attempts, "pending")
}

func (s *fakeStore) insertLocked(q runtime.QueueName, kind string, payload []byte, attempts int, status string) int64 {
	s.nextID[q]++
	id := s.nextID[q]
	s.rows[q][id] = &queueRow{
		id: id, kind: kind, payload: append([]byte(nil), payload...),
		createdAt: s.clock.Now(), status: status, attempts: attempts,
		nextAttemptAt: s.clock.Now(),
	}
	return id
}

func (s *fakeStore) status(q runtime.QueueName, id int64) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rows[q][id].status
}

func (s *fakeStore) attempts(q runtime.QueueName, id int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rows[q][id].attempts
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

type fakePuller struct {
	callRecorder

	runPullFn func(ctx context.Context) error
}

func (f *fakePuller) RunPull(ctx context.Context) error {
	f.record("RunPull")
	if f.runPullFn != nil {
		return f.runPullFn(ctx)
	}
	return nil
}

var _ runtime.Puller = (*fakePuller)(nil)

type fakeBlocker struct {
	callRecorder

	markBlockedFn func(ctx context.Context, ticketID, reason string) error
}

func (f *fakeBlocker) MarkBlocked(ctx context.Context, ticketID, reason string) error {
	f.record("MarkBlocked", ticketID, reason)
	if f.markBlockedFn != nil {
		return f.markBlockedFn(ctx, ticketID, reason)
	}
	return nil
}

var _ runtime.Blocker = (*fakeBlocker)(nil)

type fakeAgentRuntime struct {
	callRecorder

	sendFn    func(ctx context.Context, idempotencyKey int64, payload []byte) error
	releaseFn func(ctx context.Context, idempotencyKey int64, payload []byte) error
}

func (f *fakeAgentRuntime) Send(ctx context.Context, idempotencyKey int64, payload []byte) error {
	f.record("Send", idempotencyKey, append([]byte(nil), payload...))
	if f.sendFn != nil {
		return f.sendFn(ctx, idempotencyKey, payload)
	}
	return nil
}

func (f *fakeAgentRuntime) Release(ctx context.Context, idempotencyKey int64, payload []byte) error {
	f.record("Release", idempotencyKey, append([]byte(nil), payload...))
	if f.releaseFn != nil {
		return f.releaseFn(ctx, idempotencyKey, payload)
	}
	return nil
}

var _ runtime.AgentRuntime = (*fakeAgentRuntime)(nil)

type fakeNotifier struct {
	callRecorder

	sendFn func(ctx context.Context, payload []byte) error
}

func (f *fakeNotifier) Send(ctx context.Context, payload []byte) error {
	f.record("Send", append([]byte(nil), payload...))
	if f.sendFn != nil {
		return f.sendFn(ctx, payload)
	}
	return nil
}

var _ runtime.Notifier = (*fakeNotifier)(nil)

type fakeSnapshotPusher struct {
	callRecorder

	pushBoardFn func(ctx context.Context) error
}

func (f *fakeSnapshotPusher) PushBoard(ctx context.Context) error {
	f.record("PushBoard")
	if f.pushBoardFn != nil {
		return f.pushBoardFn(ctx)
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

	appendUserFn func(ctx context.Context, text string) (int64, int64, error)
	appendKilnFn func(ctx context.Context, text string) (runtime.Message, error)

	appendUserCalls int
	appendKilnCalls int
	recentCalls     []int
}

func (f *fakeMessageStore) AppendUserMessageAndEnqueueEvent(ctx context.Context, text string) (int64, int64, error) {
	f.mu.Lock()
	f.appendUserCalls++
	f.mu.Unlock()
	if f.appendUserFn != nil {
		return f.appendUserFn(ctx, text)
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

func (f *fakeMessageStore) AppendKilnMessage(ctx context.Context, text string) (runtime.Message, error) {
	f.mu.Lock()
	f.appendKilnCalls++
	f.mu.Unlock()
	if f.appendKilnFn != nil {
		return f.appendKilnFn(ctx, text)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	m := runtime.Message{ID: f.nextID, Role: runtime.RoleKiln, Text: text, CreatedAt: time.Now()}
	f.messages = append(f.messages, m)
	return m, nil
}

func (f *fakeMessageStore) Recent(_ context.Context, n int) ([]runtime.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recentCalls = append(f.recentCalls, n)
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

type fakeSayPusher struct {
	mu        sync.Mutex
	pushed    []runtime.Message
	pushSayFn func(ctx context.Context, m runtime.Message) error
}

func (f *fakeSayPusher) PushSay(ctx context.Context, m runtime.Message) error {
	f.mu.Lock()
	f.pushed = append(f.pushed, m)
	f.mu.Unlock()
	if f.pushSayFn != nil {
		return f.pushSayFn(ctx, m)
	}
	return nil
}

func (f *fakeSayPusher) pushedMessages() []runtime.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]runtime.Message, len(f.pushed))
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

	postFn    func(ctx context.Context, kind, body string, ticketID, imageURL *string) (runtime.Notification, error)
	retractFn func(ctx context.Context, id int64) error
	markSeenN []int64
	edits     []notificationEdit
}

// notificationEdit records one EditNotification call for delegation assertions.
type notificationEdit struct {
	ID       int64
	Kind     string
	Body     string
	ImageURL *string
}

func (f *fakeNotificationStore) PostNotification(
	ctx context.Context, kind, body string, ticketID, imageURL *string,
) (runtime.Notification, error) {
	if f.postFn != nil {
		return f.postFn(ctx, kind, body, ticketID, imageURL)
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
	return n, nil
}

func (f *fakeNotificationStore) RetractNotification(ctx context.Context, id int64) error {
	if f.retractFn != nil {
		return f.retractFn(ctx, id)
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

func (f *fakeNotificationStore) EditNotification(
	_ context.Context, id int64, kind, body string, imageURL *string,
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

func (f *fakeNotificationStore) MarkSeen(_ context.Context, lastID int64) error {
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

func (f *fakeNotificationStore) UnseenNotifications(_ context.Context) ([]runtime.Notification, error) {
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
	_ context.Context, limit int,
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
	_ context.Context, before int64, limit int,
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

func (f *fakeNotificationStore) LastSeenID(_ context.Context) (*int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var max *int64
	for i := range f.rows {
		if f.rows[i].SeenAt != nil && (max == nil || f.rows[i].ID > *max) {
			id := f.rows[i].ID
			max = &id
		}
	}
	return max, nil
}

func (f *fakeNotificationStore) UnseenCount(_ context.Context) (int, error) {
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

var _ runtime.NotificationStore = (*fakeNotificationStore)(nil)

// fakeBoardReader is an in-memory runtime.BoardReader returning a staged
// BoardView.
type fakeBoardReader struct {
	view    runtime.BoardView
	viewErr error
}

func (f *fakeBoardReader) BoardView(context.Context) (runtime.BoardView, error) {
	return f.view, f.viewErr
}

var _ runtime.BoardReader = (*fakeBoardReader)(nil)

// fakeFeedPusher records every pushed FeedSnapshot (08 §3).
type fakeFeedPusher struct {
	mu     sync.Mutex
	pushed []runtime.FeedSnapshot
}

func (f *fakeFeedPusher) PushFeed(_ context.Context, snap runtime.FeedSnapshot) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pushed = append(f.pushed, snap)
	return nil
}

func (f *fakeFeedPusher) pushes() []runtime.FeedSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]runtime.FeedSnapshot, len(f.pushed))
	copy(out, f.pushed)
	return out
}

var _ runtime.FeedPusher = (*fakeFeedPusher)(nil)

// fakeActivityPusher records every pushed ActivityEvent (08 §4).
type fakeActivityPusher struct {
	mu     sync.Mutex
	pushed []runtime.ActivityEvent
}

func (f *fakeActivityPusher) PushActivity(_ context.Context, ev runtime.ActivityEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pushed = append(f.pushed, ev)
	return nil
}

func (f *fakeActivityPusher) events() []runtime.ActivityEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]runtime.ActivityEvent, len(f.pushed))
	copy(out, f.pushed)
	return out
}

var _ runtime.ActivityPusher = (*fakeActivityPusher)(nil)
