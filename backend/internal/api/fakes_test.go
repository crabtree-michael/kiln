package api_test

// Shared unit-test fakes for the api module (04 §9: "unit (api): routes
// against a fake runtime/board — decode/encode, snapshot-on-connect, fan-out
// to multiple SSE clients, keepalive"). Everything here is in-memory; the
// only real network is the loopback httptest.Server the route tests dial.

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/api"
	"github.com/crabtree-michael/kiln/backend/internal/board"
	"github.com/crabtree-michael/kiln/backend/internal/identity"
	"github.com/crabtree-michael/kiln/backend/internal/runtime"
)

// testProjectID is the project every authenticated test session resolves to
// (11 phase 2). fakeProjects hands it back, and the guarded handlers pass it
// straight through to their ports, so a test can assert the project id that
// reached a fake equals this.
const testProjectID = "proj-A"

// fakeProjects is api.ProjectResolver: it resolves every caller to one canned
// project (or an injected error — identity.ErrNotFound for the no-project 404
// path) and records the user ids it was asked to resolve, so a test can assert
// withProject scoped to the resolved session's user/project.
type fakeProjects struct {
	mu      sync.Mutex
	project identity.Project
	err     error
	userIDs []string
}

func (f *fakeProjects) ProjectFor(_ context.Context, userID string) (identity.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.userIDs = append(f.userIDs, userID)
	return f.project, f.err
}

func (f *fakeProjects) resolvedUserIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.userIDs...)
}

// authCookie is the kiln_session request cookie every authenticated test call
// carries. Its value is irrelevant — the fake authenticator resolves any token
// to a valid user — so what matters is only that the cookie is present, which
// is what withSession/withProject gate on.
func authCookie() *http.Cookie {
	//nolint:gosec // G124: an outgoing request cookie the test sends, not a Set-Cookie response.
	return &http.Cookie{Name: testSessionCookie, Value: "test-session"}
}

// enableSession turns a bare *api.Server into a fully session+project-scoped
// one (11 phase 2): EnableIdentity with an authenticator that resolves any
// cookie to testUserID, and EnableTenancy with a resolver that hands back
// testProjectID. It is how every app-route test server is made to pass the
// withSession/withProject guards the routes are now wrapped in. Returns srv so
// it can wrap an inline api.NewServer(...) expression.
func enableSession(srv *api.Server) *api.Server {
	srv.EnableIdentity(&fakeAuth{resolveUser: identity.User{ID: testUserID}}, &fakeAccount{})
	srv.EnableTenancy(&fakeProjects{project: identity.Project{ID: testProjectID}})
	return srv
}

// fakeVoiceTokenMinter is api.VoiceTokenMinter (09 §6 POST /api/voice/token):
// a canned token/expiry or an injected mint error.
type fakeVoiceTokenMinter struct {
	token string
	exp   time.Time
	err   error
}

func (f *fakeVoiceTokenMinter) MintStreamingToken(context.Context) (string, time.Time, error) {
	return f.token, f.exp, f.err
}

// fakeBoardReader is api.BoardReader: a single configurable snapshot,
// returned to every caller (GET /api/board and the hub's board pushes
// share this same port, 04 §7).
type fakeBoardReader struct {
	mu            sync.Mutex
	snapshot      board.Snapshot
	err           error
	calls         int
	lastProjectID string
}

func (f *fakeBoardReader) GetBoard(_ context.Context, projectID string) (board.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastProjectID = projectID
	return f.snapshot, f.err
}

func (f *fakeBoardReader) projectID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastProjectID
}

func (f *fakeBoardReader) setSnapshot(s board.Snapshot) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snapshot = s
}

func (f *fakeBoardReader) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fakeMessagePoster is api.MessagePoster (07 §3-§4).
type fakeMessagePoster struct {
	mu            sync.Mutex
	texts         []string
	messageID     int64
	eventID       int64
	err           error
	lastProjectID string
}

func (f *fakeMessagePoster) PostMessage(_ context.Context, projectID, text string) (int64, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.texts = append(f.texts, text)
	f.lastProjectID = projectID
	return f.messageID, f.eventID, f.err
}

func (f *fakeMessagePoster) projectID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastProjectID
}

func (f *fakeMessagePoster) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.texts)
}

func (f *fakeMessagePoster) lastText() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.texts) == 0 {
		return ""
	}
	return f.texts[len(f.texts)-1]
}

// fakeMessagesReader is api.MessagesReader (07 §4 GET /api/messages).
type fakeMessagesReader struct {
	mu       sync.Mutex
	messages []runtime.Message
	err      error
	ns       []int
}

func (f *fakeMessagesReader) Recent(_ context.Context, _ string, n int) ([]runtime.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ns = append(f.ns, n)
	if f.err != nil {
		return nil, f.err
	}
	if n >= len(f.messages) {
		out := make([]runtime.Message, len(f.messages))
		copy(out, f.messages)
		return out, nil
	}
	out := make([]runtime.Message, n)
	copy(out, f.messages[len(f.messages)-n:])
	return out, nil
}

func (f *fakeMessagesReader) requestedNs() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.ns...)
}

// fakeFeedReader is api.FeedReader (08 §3, D2′ GET /api/feed + /api/feed/history).
type fakeFeedReader struct {
	mu       sync.Mutex
	snapshot runtime.FeedSnapshot
	err      error
	calls    int

	// history is served by FeedHistory; historyMore is its has-more flag, and
	// historyBefore/historyLimit record the last paging request for assertions.
	history       []runtime.FeedCard
	historyMore   bool
	historyErr    error
	historyBefore int64
	historyLimit  int
	historyCalls  int
}

func (f *fakeFeedReader) Feed(_ context.Context, _ string) (runtime.FeedSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.snapshot, f.err
}

func (f *fakeFeedReader) FeedHistory(
	_ context.Context, _ string, before int64, limit int,
) ([]runtime.FeedCard, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.historyCalls++
	f.historyBefore = before
	f.historyLimit = limit
	return f.history, f.historyMore, f.historyErr
}

func (f *fakeFeedReader) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fakeSeenAcker is api.FeedMutator (08 §3: POST /api/feed/seen +
// POST /api/feed/{id}/dismiss).
type fakeSeenAcker struct {
	mu             sync.Mutex
	lastIDs        []int64
	dismissedIDs   []int64
	dismissAllCall int
	err            error
}

func (f *fakeSeenAcker) MarkSeen(_ context.Context, _ string, lastID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastIDs = append(f.lastIDs, lastID)
	return f.err
}

func (f *fakeSeenAcker) DismissNotification(_ context.Context, _ string, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dismissedIDs = append(f.dismissedIDs, id)
	return f.err
}

func (f *fakeSeenAcker) DismissAllNotifications(_ context.Context, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dismissAllCall++
	return f.err
}

func (f *fakeSeenAcker) dismissedAll() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dismissAllCall
}

func (f *fakeSeenAcker) dismissed() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), f.dismissedIDs...)
}

func (f *fakeSeenAcker) seen() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), f.lastIDs...)
}

// devTicketID is the id fakeSeeder mints, shared with the route assertions.
const devTicketID = "t-1"

// fakeSeeder is the double for the dev-only TicketSeeder port: it records the
// SeedTicket spec and can inject a failure (including board.ErrNoFreeWorker).
type fakeSeeder struct {
	seedErr       error
	spec          board.SeedSpec
	markedReady   board.TicketID
	lastProjectID string
}

func (f *fakeSeeder) SeedTicket(_ context.Context, projectID string, spec board.SeedSpec) (board.Ticket, error) {
	if f.seedErr != nil {
		return board.Ticket{}, f.seedErr
	}
	f.lastProjectID = projectID
	f.spec = spec
	state := spec.State
	if state == "" {
		state = board.StateShaping
	}
	return board.Ticket{
		ID: devTicketID, Title: spec.Title, Body: spec.Body, State: state,
		ApprovalRequested: spec.ApprovalRequested,
	}, nil
}

// MarkReady records the ready transition the dev route triggers for a state=ready
// seed and returns the ticket in ready.
func (f *fakeSeeder) MarkReady(_ context.Context, projectID string, id board.TicketID) (board.Ticket, error) {
	if f.seedErr != nil {
		return board.Ticket{}, f.seedErr
	}
	f.lastProjectID = projectID
	f.markedReady = id
	return board.Ticket{ID: id, Title: f.spec.Title, Body: f.spec.Body, State: board.StateReady}, nil
}

// fakeNotificationPoster is the double for the dev-only NotificationPoster port
// (08 §E.3 POST /api/dev/notifications).
type fakeNotificationPoster struct {
	mu        sync.Mutex
	calls     []devNote
	err       error
	projectID string
}

type devNote struct {
	kind, body         string
	ticketID, imageURL *string
}

func (f *fakeNotificationPoster) PostNotification(
	_ context.Context, projectID, kind, body string, ticketID, imageURL *string,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.projectID = projectID
	f.calls = append(f.calls, devNote{kind: kind, body: body, ticketID: ticketID, imageURL: imageURL})
	return f.err
}

func (f *fakeNotificationPoster) posted() []devNote {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]devNote(nil), f.calls...)
}

// fakeAuth is api.Authenticator (11 §2 GitHub OAuth + cookie sessions): a
// scripted double covering the whole login/session/logout lifecycle, with
// call recording so the route tests can assert what reached the port.
type fakeAuth struct {
	mu sync.Mutex

	loginURL string // base URL LoginURL appends "?state=" onto

	completeLoginUser  identity.User
	completeLoginErr   error
	completeLoginCalls []string // codes CompleteLogin was called with, in order

	sessionToken     string
	sessionExpires   time.Time
	createSessionErr error

	resolveUser    identity.User
	resolveExpires time.Time // zero unless a test cares about the re-issued cookie's Max-Age
	resolveErr     error

	logoutErr   error
	logoutCalls []string // tokens Logout was called with, in order
}

func (f *fakeAuth) LoginURL(state string) string {
	return f.loginURL + "?state=" + state
}

func (f *fakeAuth) CompleteLogin(_ context.Context, code string) (identity.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completeLoginCalls = append(f.completeLoginCalls, code)
	return f.completeLoginUser, f.completeLoginErr
}

func (f *fakeAuth) CreateSession(_ context.Context, _ string) (string, time.Time, error) {
	return f.sessionToken, f.sessionExpires, f.createSessionErr
}

func (f *fakeAuth) ResolveSession(_ context.Context, _ string) (identity.User, time.Time, error) {
	return f.resolveUser, f.resolveExpires, f.resolveErr
}

func (f *fakeAuth) Logout(_ context.Context, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logoutCalls = append(f.logoutCalls, token)
	return f.logoutErr
}

func (f *fakeAuth) completeLoginCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.completeLoginCalls)
}

func (f *fakeAuth) lastCompleteLoginCode() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.completeLoginCalls) == 0 {
		return ""
	}
	return f.completeLoginCalls[len(f.completeLoginCalls)-1]
}

func (f *fakeAuth) logoutCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.logoutCalls)
}

func (f *fakeAuth) lastLogoutToken() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.logoutCalls) == 0 {
		return ""
	}
	return f.logoutCalls[len(f.logoutCalls)-1]
}

// fakeAccount is api.AccountService (11 §4 GET /api/me, PUT /api/settings,
// PUT /api/project, POST /api/settings/verify): a scripted double recording
// every write's arguments so the route tests can assert what reached the
// port, alongside canned Me/Verify results.
type fakeAccount struct {
	mu sync.Mutex

	me    identity.Me
	meErr error

	settingsUpd  identity.SettingsUpdate
	settingsErr  error
	settingsCall int

	projectUpd    identity.ProjectUpdate
	projectResult identity.Project
	projectErr    error
	projectCall   int

	verifyChecks []identity.CheckResult
	verifyErr    error
}

func (f *fakeAccount) Me(context.Context, string) (identity.Me, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.me, f.meErr
}

func (f *fakeAccount) UpdateSettings(_ context.Context, _ string, upd identity.SettingsUpdate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settingsUpd = upd
	f.settingsCall++
	return f.settingsErr
}

func (f *fakeAccount) UpsertProject(_ context.Context, _ string, upd identity.ProjectUpdate) (identity.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.projectUpd = upd
	f.projectCall++
	return f.projectResult, f.projectErr
}

func (f *fakeAccount) Verify(context.Context, string) ([]identity.CheckResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.verifyChecks, f.verifyErr
}

func (f *fakeAccount) lastSettingsUpdate() identity.SettingsUpdate {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.settingsUpd
}

func (f *fakeAccount) lastProjectUpdate() identity.ProjectUpdate {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.projectUpd
}

// fakeDevSession is api.DevSessionMinter (11 §7 POST /api/dev/session): mint a
// session for a dev-supplied GitHub login without the real OAuth dance.
type fakeDevSession struct {
	mu sync.Mutex

	signInLogins []string
	signInUser   identity.User
	signInErr    error

	sessionToken     string
	sessionExpires   time.Time
	createSessionErr error
}

func (f *fakeDevSession) DevSignIn(_ context.Context, login string) (identity.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signInLogins = append(f.signInLogins, login)
	return f.signInUser, f.signInErr
}

func (f *fakeDevSession) CreateSession(context.Context, string) (string, time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sessionToken, f.sessionExpires, f.createSessionErr
}
