//go:build integration

// Cross-tenant isolation over the real API surface (11 phase 2, spec §8
// headline): two tenants, each with their own user + project seeded through
// the real identity service against TEST_DATABASE_URL, driving a real
// api.Server wired over real postgres stores (board/runtime/identity) and a
// real ProjectResolver (identity). The brain/agent providers are absent — these
// tests exercise the read/write/SSE API surface, not the brain loop — so no
// worker runs; every mutation here is a direct client request.
//
// The suite proves the four guarantees a tenancy flip must uphold:
//  1. A read never returns another tenant's rows (board/feed/messages/history).
//  2. A write keyed by another tenant's id never mutates that tenant's data.
//  3. An SSE stream never receives another tenant's fan-out frame.
//  4. The app surface is closed: no session ⇒ 401; a session with no project
//     ⇒ 404.
//
// Run:
//
//	TEST_DATABASE_URL=postgres://kiln:kiln@localhost:5432/kiln_test?sslmode=disable \
//	    go test -tags=integration ./internal/api/... -run Tenancy
//
// kiln_test is shared with the per-module integration tests; setup only ever
// creates a module's own tables when missing and only ever truncates the tables
// this suite touches — never DROPs.
package api_test

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path"
	"sort"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/crabtree-michael/kiln/backend/internal/api"
	"github.com/crabtree-michael/kiln/backend/internal/board"
	boardpg "github.com/crabtree-michael/kiln/backend/internal/board/postgres"
	"github.com/crabtree-michael/kiln/backend/internal/identity"
	identitypg "github.com/crabtree-michael/kiln/backend/internal/identity/postgres"
	"github.com/crabtree-michael/kiln/backend/internal/runtime"
	runtimepg "github.com/crabtree-michael/kiln/backend/internal/runtime/postgres"
	"github.com/crabtree-michael/kiln/backend/internal/wire"
)

// tenCipherKey is a fixed 64-hex (32-byte) key for the identity cipher; the
// isolation tests never store real secrets, so a constant test key is fine.
const tenCipherKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// ---- harness ---------------------------------------------------------------

// tenServer is the wired real server under test plus the handles a test needs
// to seed tenants (idSvc) and to simulate a project's SSE fan-out (hub).
type tenServer struct {
	ts    *httptest.Server
	idSvc *identity.Service
	hub   *api.Hub
	db    *sql.DB
}

// newTenServer builds the real object graph: identity + board + runtime stores
// over TEST_DATABASE_URL, a hub over the board reader, and an api.Server with
// every app route mounted (identity, tenancy, dev-session, dev-tickets,
// dev-notifications) so a single server exercises the whole gated surface.
func newTenServer(t *testing.T) *tenServer {
	t.Helper()
	db := tenDB(t)

	cipher, err := identity.NewCipher(tenCipherKey)
	if err != nil {
		t.Fatalf("identity cipher: %v", err)
	}
	// gh is nil and allowedLogins empty: the isolation tests reach identity only
	// through DevSignIn/EnsureUser/UpsertProject/ProjectFor/CreateSession/
	// ResolveSession, none of which touch the GitHub client or the allowlist.
	idSvc := identity.NewService(identitypg.New(db), cipher, nil, nil)

	boardSvc := board.NewService(boardpg.New(db))
	rtStore := runtimepg.New(db)
	hub := api.NewHub(boardSvc)
	rtSvc := runtime.NewService(
		rtStore, rtStore, nil, nil, nil,
		nil, nil, nil, hub, hub,
		rtStore, &tenBoardView{inner: boardSvc}, hub, hub,
		nil,
	)

	srv := api.NewServer(boardSvc, rtSvc, rtSvc, rtSvc, rtSvc, hub, nil)
	srv.EnableIdentity(idSvc, idSvc)
	srv.EnableTenancy(idSvc)
	srv.EnableDevSession(idSvc)
	srv.EnableDevTickets(boardSvc)
	srv.EnableDevNotifications(rtSvc)

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &tenServer{ts: ts, idSvc: idSvc, hub: hub, db: db}
}

// tenBoardView satisfies runtime.BoardReader over *board.Service for feed
// assembly (mirrors cmd/kiln's boardViewAdapter): blocked tickets → blocker
// cards, shaping tickets → proposal cards, plus the id→title index and the
// working/blocked counts the feed summary needs.
type tenBoardView struct{ inner *board.Service }

func (a *tenBoardView) BoardView(ctx context.Context, projectID string) (runtime.BoardView, error) {
	snap, err := a.inner.GetBoard(ctx, projectID)
	if err != nil {
		return runtime.BoardView{}, fmt.Errorf("tenBoardView: %w", err)
	}
	view := runtime.BoardView{
		WorkingCount: len(snap.Working),
		BlockedCount: len(snap.Blocked),
		TicketTitles: map[string]string{},
	}
	for _, group := range [][]board.Ticket{snap.Shaping, snap.Ready, snap.Blocked, snap.Working, snap.Done} {
		for _, tk := range group {
			view.TicketTitles[string(tk.ID)] = tk.Title
		}
	}
	for _, tk := range snap.Blocked {
		reason := ""
		if tk.BlockedReason != nil {
			reason = *tk.BlockedReason
		}
		view.Blocked = append(view.Blocked, runtime.BoardTicket{
			ID: string(tk.ID), Title: tk.Title, Body: tk.Body, BlockedReason: reason, UpdatedAt: tk.UpdatedAt,
		})
	}
	for _, tk := range snap.Shaping {
		view.Proposals = append(view.Proposals, runtime.BoardTicket{
			ID: string(tk.ID), Title: tk.Title, Body: tk.Body, UpdatedAt: tk.UpdatedAt,
		})
	}
	return view, nil
}

var _ runtime.BoardReader = (*tenBoardView)(nil)

// ---- DB setup --------------------------------------------------------------

// tenDB opens TEST_DATABASE_URL, ensures the identity/board/runtime schemas
// exist, and truncates exactly the tables this suite touches.
func tenDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run the api tenancy integration tests")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open %s: %v", dsn, err)
	}
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Logf("close db: %v", cerr)
		}
	})
	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	// Apply each module's embedded migrations only if its sentinel table is
	// missing — kiln_test is shared, so never DROP and never touch tables owned
	// by modules this suite doesn't seed.
	tenEnsureSchema(ctx, t, db, "projects", identitypg.Migrations)
	tenEnsureSchema(ctx, t, db, "tickets", boardpg.Migrations)
	tenEnsureSchema(ctx, t, db, "notifications", runtimepg.Migrations)

	if _, err := db.ExecContext(ctx,
		`TRUNCATE TABLE users, sessions, user_config, projects,
			tickets, workers, outbox, events, messages, notifications
			RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate tenancy tables: %v", err)
	}
	return db
}

// tenEnsureSchema applies migrationsFS (rooted at a "migrations" subdir) in
// filename order when `sentinel` does not yet exist, so a fresh kiln_test is
// bootstrapped without disturbing an already-migrated shared DB.
func tenEnsureSchema(ctx context.Context, t *testing.T, db *sql.DB, sentinel string, migrationsFS fs.FS) {
	t.Helper()
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = $1
	)`, sentinel).Scan(&exists); err != nil {
		t.Fatalf("check for %s table: %v", sentinel, err)
	}
	if exists {
		return
	}
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("sub migrations: %v", err)
	}
	entries, err := fs.ReadDir(sub, ".")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && path.Ext(e.Name()) == ".sql" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		body, err := fs.ReadFile(sub, name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		if _, err := db.ExecContext(ctx, string(body)); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}
}

// ---- tenant handle ---------------------------------------------------------

// tenant is one signed-in tenant: an HTTP client whose cookie jar carries the
// kiln_session minted for its user, plus the ids of the user/project it owns.
type tenant struct {
	client    *http.Client
	baseURL   string
	userID    string
	projectID string
	login     string
}

// signInTenant creates the user (find-or-create) and mints its session cookie
// via the real POST /api/dev/session. The project is created separately (or
// not, for the no-project case) so a caller can also mint an onboarding-pending
// tenant.
func signInTenant(t *testing.T, s *tenServer, login string) *tenant {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	client := &http.Client{Jar: jar}
	tn := &tenant{client: client, baseURL: s.ts.URL, login: login}

	resp := tn.do(t, http.MethodPost, "/api/dev/session", map[string]string{"github_login": login})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/dev/session (%s) status = %d, want 200", login, resp.StatusCode)
	}
	closeBody(t, resp)

	// Resolve the user id the login maps to (find-or-create is deterministic).
	u, err := s.idSvc.EnsureUser(context.Background(), login)
	if err != nil {
		t.Fatalf("EnsureUser(%s): %v", login, err)
	}
	tn.userID = u.ID
	return tn
}

// onboard creates the tenant's single project, so its session resolves past
// withProject's no-project 404.
func (tn *tenant) onboard(t *testing.T, s *tenServer, name string) {
	t.Helper()
	p, err := s.idSvc.UpsertProject(context.Background(), tn.userID, identity.ProjectUpdate{
		Name:        name,
		RepoURL:     "https://github.com/kiln-test/" + name,
		WorkerCount: 3,
	})
	if err != nil {
		t.Fatalf("UpsertProject(%s): %v", name, err)
	}
	tn.projectID = p.ID
}

// do issues a JSON request through the tenant's jar-bearing client. A nil body
// sends no body; the caller owns closing the response.
func (tn *tenant) do(t *testing.T, method, pathStr string, body any) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(string(mustJSON(t, body)))
	}
	req, err := http.NewRequestWithContext(context.Background(), method, tn.baseURL+pathStr, reader)
	if err != nil {
		t.Fatalf("build %s %s: %v", method, pathStr, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := tn.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, pathStr, err)
	}
	return resp
}

// getJSON does a GET and decodes the JSON body into out, asserting 200.
func (tn *tenant) getJSON(t *testing.T, pathStr string, out any) {
	t.Helper()
	resp := tn.do(t, http.MethodGet, pathStr, nil)
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200", pathStr, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("decode %s: %v", pathStr, err)
	}
}

// seedTicket seeds a ticket into the tenant's board via the dev route and
// returns its id.
func (tn *tenant) seedTicket(t *testing.T, title, body, state string) string {
	t.Helper()
	resp := tn.do(t, http.MethodPost, "/api/dev/tickets", map[string]string{
		"title": title, "body": body, "state": state,
	})
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/dev/tickets status = %d, want 201", resp.StatusCode)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode dev ticket: %v", err)
	}
	return out.ID
}

// boardTicketIDs flattens every ticket id in a wire.Board across all columns.
func boardTicketIDs(b wire.Board) []string {
	var ids []string
	for _, g := range [][]wire.Ticket{b.Shaping, b.Ready, b.Blocked, b.Working, b.Done} {
		for _, tk := range g {
			ids = append(ids, tk.Id)
		}
	}
	return ids
}

// ---- 1. reads never cross tenants ------------------------------------------

// TestTenancy_ReadsNeverCrossTenants seeds a ticket, a feed notification, and a
// transcript message under tenant B, then asserts tenant A's board/feed/
// messages/history are empty of every one of B's rows — and, as a positive
// control, that B itself sees all three, so A's emptiness is real isolation and
// not an empty database.
func TestTenancy_ReadsNeverCrossTenants(t *testing.T) {
	s := newTenServer(t)
	a := signInTenant(t, s, "tenant-a")
	a.onboard(t, s, "proj-a")
	b := signInTenant(t, s, "tenant-b")
	b.onboard(t, s, "proj-b")

	bTicketID := b.seedTicket(t, "B-secret-ticket", "B's private work order", "shaping")

	resp := b.do(t, http.MethodPost, "/api/dev/notifications", map[string]any{
		"kind": "update", "body": "B-secret-note",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("B POST /api/dev/notifications status = %d, want 201", resp.StatusCode)
	}
	closeBody(t, resp)

	resp = b.do(t, http.MethodPost, "/api/message", map[string]string{"text": "B-secret-message"})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("B POST /api/message status = %d, want 202", resp.StatusCode)
	}
	closeBody(t, resp)

	// Positive control: B sees its own data.
	var bBoard wire.Board
	b.getJSON(t, "/api/board", &bBoard)
	if ids := boardTicketIDs(bBoard); len(ids) != 1 || ids[0] != bTicketID {
		t.Fatalf("B's own board ids = %v, want exactly [%s] — seeding must have landed", ids, bTicketID)
	}
	var bFeed wire.FeedSnapshot
	b.getJSON(t, "/api/feed", &bFeed)
	if !feedHasBody(bFeed, "B-secret-note") {
		t.Fatalf("B's own feed is missing B-secret-note: %+v", bFeed.Cards)
	}
	var bMsgs []wire.Message
	b.getJSON(t, "/api/messages", &bMsgs)
	if !messagesHaveText(bMsgs, "B-secret-message") {
		t.Fatalf("B's own transcript is missing B-secret-message: %+v", bMsgs)
	}

	// Isolation: A sees none of it.
	var aBoard wire.Board
	a.getJSON(t, "/api/board", &aBoard)
	if ids := boardTicketIDs(aBoard); len(ids) != 0 {
		t.Errorf("A's board = %v, want empty — B's ticket %s leaked across the tenant boundary", ids, bTicketID)
	}

	var aFeed wire.FeedSnapshot
	a.getJSON(t, "/api/feed", &aFeed)
	if len(aFeed.Cards) != 0 {
		t.Errorf("A's feed = %+v, want no cards — B's blocker/proposal/update cards leaked", aFeed.Cards)
	}
	if feedHasBody(aFeed, "B-secret-note") {
		t.Errorf("A's feed contains B-secret-note — notification isolation breached")
	}

	var aMsgs []wire.Message
	a.getJSON(t, "/api/messages", &aMsgs)
	if len(aMsgs) != 0 {
		t.Errorf("A's transcript = %+v, want empty — B's message leaked", aMsgs)
	}

	var aHist wire.FeedHistoryPage
	a.getJSON(t, "/api/feed/history", &aHist)
	if len(aHist.Cards) != 0 {
		t.Errorf("A's feed history = %+v, want empty — B's retained notifications leaked", aHist.Cards)
	}
}

func feedHasBody(s wire.FeedSnapshot, body string) bool {
	for _, c := range s.Cards {
		if c.Body == body {
			return true
		}
	}
	return false
}

func messagesHaveText(ms []wire.Message, text string) bool {
	for _, m := range ms {
		if m.Text == text {
			return true
		}
	}
	return false
}

// ---- 2. writes keyed by a foreign id never mutate the other tenant ---------

// TestTenancy_ForeignWritesDoNotMutateOtherTenant proves that tenant A driving
// accept/dismiss with tenant B's ticket/notification ids never changes B's
// data. Both routes are project-scoped: accept posts a scoped message to A's
// own project (spec 08 routes accept through the brain, so it 202s and falls
// back to the id when the ticket isn't in the caller's board) and dismiss is a
// scoped, idempotent no-op — so neither 404s, but crucially neither touches B.
// The tenancy guarantee asserted here is that B's ticket and notification are
// byte-for-byte unchanged and A never gains visibility into either.
func TestTenancy_ForeignWritesDoNotMutateOtherTenant(t *testing.T) {
	s := newTenServer(t)
	a := signInTenant(t, s, "tenant-a")
	a.onboard(t, s, "proj-a")
	b := signInTenant(t, s, "tenant-b")
	b.onboard(t, s, "proj-b")

	bTicketID := b.seedTicket(t, "B-ticket", "B's work order", "shaping")
	resp := b.do(t, http.MethodPost, "/api/dev/notifications", map[string]any{"kind": "update", "body": "B-note"})
	closeBody(t, resp)
	bNotifID := tenNotificationID(t, s.db, b.projectID)

	beforeState := tenTicketState(t, s.db, bTicketID)

	// A accepts B's ticket id.
	acc := a.do(t, http.MethodPost, "/api/tickets/"+bTicketID+"/accept", nil)
	acc.Body.Close()
	if acc.StatusCode == http.StatusInternalServerError {
		t.Errorf("A accept of B's ticket = 500, want a scoped no-op response, not a server error")
	}

	// A dismisses B's notification id.
	dis := a.do(t, http.MethodPost, fmt.Sprintf("/api/feed/%d/dismiss", bNotifID), nil)
	dis.Body.Close()
	if dis.StatusCode == http.StatusInternalServerError {
		t.Errorf("A dismiss of B's notification = 500, want a scoped no-op response, not a server error")
	}

	// B's ticket state is unchanged.
	if after := tenTicketState(t, s.db, bTicketID); after != beforeState {
		t.Errorf("B's ticket state changed from %q to %q after A's cross-tenant accept — WRITE LEAK", beforeState, after)
	}
	// B's notification is still active (not retracted by A's dismiss).
	if tenNotificationRetracted(t, s.db, bNotifID) {
		t.Errorf("B's notification %d was retracted by A's cross-tenant dismiss — WRITE LEAK", bNotifID)
	}
	// And B still sees its own note in the feed.
	var bFeed wire.FeedSnapshot
	b.getJSON(t, "/api/feed", &bFeed)
	if !feedHasBody(bFeed, "B-note") {
		t.Errorf("B's note disappeared after A's dismiss: %+v", bFeed.Cards)
	}

	// A's board never gained B's ticket via the accept path.
	var aBoard wire.Board
	a.getJSON(t, "/api/board", &aBoard)
	if ids := boardTicketIDs(aBoard); len(ids) != 0 {
		t.Errorf("A's board = %v after accept, want empty — accept must not surface B's ticket", ids)
	}
}

// ---- 3. SSE fan-out never crosses tenants ----------------------------------

// TestTenancy_StreamNeverReceivesOtherTenantsFrame subscribes A to /api/stream,
// drains its connect snapshot, then simulates B's board fan-out (the hub push a
// board.updated outbox entry would drive) and asserts A receives no frame for
// it within a short window. A positive control — pushing A's own board — then
// confirms A's stream is live and the fan-out works, so the silence on B's push
// is real partitioning, not a dead connection.
func TestTenancy_StreamNeverReceivesOtherTenantsFrame(t *testing.T) {
	s := newTenServer(t)
	a := signInTenant(t, s, "tenant-a")
	a.onboard(t, s, "proj-a")
	b := signInTenant(t, s, "tenant-b")
	b.onboard(t, s, "proj-b")

	stream := a.connectStream(t)
	defer stream.close()
	if _, ok := stream.next(2 * time.Second); !ok {
		t.Fatal("A: no initial board snapshot on connect")
	}

	// B's board changes and fans out — A must not see it.
	if err := s.hub.PushBoard(context.Background(), b.projectID); err != nil {
		t.Fatalf("PushBoard(B): %v", err)
	}
	if ev, ok := stream.next(500 * time.Millisecond); ok {
		t.Errorf("A's stream received a frame for B's board push: %+v — SSE fan-out crossed tenants", ev)
	}

	// Positive control: A's own board push reaches A.
	if err := s.hub.PushBoard(context.Background(), a.projectID); err != nil {
		t.Fatalf("PushBoard(A): %v", err)
	}
	ev, ok := stream.next(2 * time.Second)
	if !ok {
		t.Fatal("A's stream did not receive its own board push — stream is dead, control invalid")
	}
	if ev.name != "board" {
		t.Errorf("A's own push event name = %q, want board", ev.name)
	}
}

// tenStream is a connected /api/stream reader for one tenant. A single
// background goroutine parses frames off the response body onto `frames`, so
// next() never spawns competing readers on the same stream — a timed-out read
// (the negative control) must not leak a goroutine that steals the next frame.
type tenStream struct {
	t      *testing.T
	resp   *http.Response
	frames chan sseEvent
}

func (tn *tenant) connectStream(t *testing.T) *tenStream {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, tn.baseURL+"/api/stream", nil)
	if err != nil {
		t.Fatalf("build stream request: %v", err)
	}
	resp, err := tn.client.Do(req)
	if err != nil {
		t.Fatalf("GET /api/stream: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		closeBody(t, resp)
		t.Fatalf("GET /api/stream status = %d, want 200", resp.StatusCode)
	}
	s := &tenStream{t: t, resp: resp, frames: make(chan sseEvent, 32)}
	go s.read()
	return s
}

// read is the single frame-parsing loop: assemble `event:`/`data:` lines into
// frames (skipping keepalive comment lines) until the body closes.
func (s *tenStream) read() {
	reader := bufio.NewReader(s.resp.Body)
	var ev sseEvent
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			close(s.frames)
			return
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "event:"):
			ev.name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			ev.data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		case line == "":
			if ev.name != "" {
				s.frames <- ev
				ev = sseEvent{}
			}
		}
	}
}

func (s *tenStream) close() {
	if err := s.resp.Body.Close(); err != nil {
		s.t.Logf("close stream: %v", err)
	}
}

// next returns the next parsed SSE frame, or ok=false on timeout.
func (s *tenStream) next(timeout time.Duration) (sseEvent, bool) {
	s.t.Helper()
	select {
	case ev, ok := <-s.frames:
		return ev, ok
	case <-time.After(timeout):
		return sseEvent{}, false
	}
}

// ---- 4. the app surface is closed ------------------------------------------

// TestTenancy_UnauthenticatedAndNoProjectAreClosed asserts the surface's outer
// gates end to end: a cookieless caller is 401 on an app route, and an
// authenticated caller who has not onboarded a project is 404
// {"error":"no project configured"}.
func TestTenancy_UnauthenticatedAndNoProjectAreClosed(t *testing.T) {
	s := newTenServer(t)

	// No cookie ⇒ 401.
	noAuth := &http.Client{}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, s.ts.URL+"/api/board", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := noAuth.Do(req)
	if err != nil {
		t.Fatalf("GET /api/board (no cookie): %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		closeBody(t, resp)
		t.Fatalf("GET /api/board without a session = %d, want 401", resp.StatusCode)
	}
	closeBody(t, resp)

	// Authenticated but no project ⇒ 404 {"error":"no project configured"}.
	c := signInTenant(t, s, "tenant-c-no-project")
	resp = c.do(t, http.MethodGet, "/api/board", nil)
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /api/board (no project) = %d, want 404", resp.StatusCode)
	}
	var errBody map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&errBody); err != nil {
		t.Fatalf("decode no-project body: %v", err)
	}
	if errBody["error"] != "no project configured" {
		t.Errorf("no-project body = %v, want {\"error\":\"no project configured\"}", errBody)
	}
}

// ---- DB probes -------------------------------------------------------------

func tenTicketState(t *testing.T, db *sql.DB, ticketID string) string {
	t.Helper()
	var state string
	if err := db.QueryRowContext(context.Background(),
		`SELECT state FROM tickets WHERE id = $1`, ticketID).Scan(&state); err != nil {
		t.Fatalf("read ticket %s state: %v", ticketID, err)
	}
	return state
}

func tenNotificationID(t *testing.T, db *sql.DB, projectID string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRowContext(context.Background(),
		`SELECT id FROM notifications WHERE project_id = $1 ORDER BY id DESC LIMIT 1`, projectID).Scan(&id); err != nil {
		t.Fatalf("read notification id for project %s: %v", projectID, err)
	}
	return id
}

func tenNotificationRetracted(t *testing.T, db *sql.DB, id int64) bool {
	t.Helper()
	var retracted bool
	if err := db.QueryRowContext(context.Background(),
		`SELECT retracted_at IS NOT NULL FROM notifications WHERE id = $1`, id).Scan(&retracted); err != nil {
		t.Fatalf("read notification %d retracted: %v", id, err)
	}
	return retracted
}
