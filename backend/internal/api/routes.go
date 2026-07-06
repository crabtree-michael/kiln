package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/board"
	"github.com/crabtree-michael/kiln/backend/internal/identity"
	"github.com/crabtree-michael/kiln/backend/internal/runtime"
	"github.com/crabtree-michael/kiln/backend/internal/wire"
)

// Message length and pagination bounds, mirroring schema/openapi.yaml's
// MessageRequest (1–4000 chars) and GetMessages limit (1–500, default 50).
const (
	minMessageLen = 1
	maxMessageLen = 4000
	defaultLimit  = 50
	minLimit      = 1
	maxLimit      = 500
)

// Feed-history pagination bounds (08 D2′ GET /api/feed/history), mirroring
// schema/openapi.yaml: default page 30, bounds 1–100.
const (
	defaultFeedLimit = 30
	minFeedLimit     = 1
	maxFeedLimit     = 100
)

// healthPingTimeout bounds the DB ping behind GET /healthz so a wedged database
// fails the probe promptly instead of hanging Render's health check.
const healthPingTimeout = 3 * time.Second

// maxMessageBody caps the POST /api/message request body before it is
// decoded, so a hostile client cannot force the server to buffer an
// arbitrarily large body into memory (the maxMessageLen check only runs after
// a full decode). 64 KiB comfortably admits any valid MessageRequest — a
// 4000-char text plus JSON/UTF-8 escaping overhead — while rejecting anything
// larger up front.
const maxMessageBody = 64 << 10

// maxPushBody caps the POST /api/push/subscribe body before decoding. A browser
// PushSubscription (endpoint URL + two short base64url keys) is well under 8 KiB.
const maxPushBody = 8 << 10

// BoardReader is the api's port onto the board's read path (03 §4 GetBoard).
type BoardReader interface {
	GetBoard(ctx context.Context) (board.Snapshot, error)
}

// AgentInspector is the api's read seam onto live worker status, joined into
// the board snapshot for the Streams view (amended 2026-07-05). Satisfied by a
// cmd/kiln adapter over *agent.Service — the api never imports internal/agent,
// so AgentInfo mirrors the agent module's shape by value (same rule the brain
// follows). Optional: a nil inspector yields an empty agents array (the board
// still renders).
type AgentInspector interface {
	ListAgents(ctx context.Context) ([]AgentInfo, error)
}

// AgentInfo is one live worker's status joined to its most-recent ticket
// binding — the api-local mirror of agent.AgentInfo (amended 2026-07-05).
// Status is the neutral running state (building|idle|stopped|errored|starting);
// TicketID is "" for an idle-pool worker.
type AgentInfo struct {
	WorkerID string
	TicketID string
	Status   string
}

// MessagePoster is the api's port onto the runtime's transactional message
// ingestion (07 §3–§4, amending 04 §7's POST /api/message): append the user
// transcript row and enqueue the human.message event {text} in one
// transaction — the transcript and the event queue cannot disagree.
// Satisfied directly by *runtime.Service's PostMessage.
type MessagePoster interface {
	PostMessage(ctx context.Context, text string) (messageID, eventID int64, err error)
}

// MessagesReader is the api's port onto the persisted transcript (07 §4 GET
// /api/messages): the most-recent n rows, oldest first. Satisfied directly
// by *runtime.Service's Recent.
type MessagesReader interface {
	Recent(ctx context.Context, n int) ([]runtime.Message, error)
}

// FeedReader is the api's port onto the runtime's feed assembly (08 §3, D2′):
// the absolute FeedSnapshot for GET /api/feed (blockers, proposals, newest page
// of retained updates) and one older keyset page for GET /api/feed/history.
// Satisfied directly by *runtime.Service's Feed / FeedHistory.
type FeedReader interface {
	Feed(ctx context.Context) (runtime.FeedSnapshot, error)
	// FeedHistory returns update/preview cards older than `before` (newest-first,
	// up to `limit`) and whether a further page remains (08 D2′).
	FeedHistory(ctx context.Context, before int64, limit int) ([]runtime.FeedCard, bool, error)
}

// FeedMutator is the api's port onto the client-driven feed mutations (08 §3):
// advancing the seen high-water mark (POST /api/feed/seen), clearing a single
// card by swipe (POST /api/feed/{id}/dismiss), and clearing them all at once
// (POST /api/feed/dismiss-all). Satisfied directly by *runtime.Service's
// MarkSeen / DismissNotification / DismissAllNotifications.
type FeedMutator interface {
	// MarkSeen marks every update up to and including lastID as seen.
	MarkSeen(ctx context.Context, lastID int64) error
	// DismissNotification clears one update/preview card for good by its
	// notification id (user swiped it away). Idempotent on an unknown/gone id.
	DismissNotification(ctx context.Context, id int64) error
	// DismissAllNotifications clears every feed notification at once (the user's
	// "clear all" header trash affordance). Idempotent: a no-op when none active.
	DismissAllNotifications(ctx context.Context) error
}

// TicketSeeder is the DEV-ONLY port behind POST /api/dev/tickets: seed a ticket
// straight into a target state (08 §B.6 SeedSpec), bypassing the brain's
// create/shape decision (D5). It exists so an e2e can establish a feed/board
// precondition deterministically — a Developing ticket, a blocker card, a
// proposal card — and then exercise the real loop. Satisfied by *board.Service.
// Mounted only when EnableDevTickets was called; NOT part of the wire contract
// (/schema) — never the real client.
type TicketSeeder interface {
	SeedTicket(ctx context.Context, spec board.SeedSpec) (board.Ticket, error)
	// MarkReady is the real board op, reused by the dev route for a state=ready
	// seed: a direct insert into ready would be inert (no pull.evaluate, no
	// queued toast), so ready is seeded as shaping then marked ready, exactly
	// like the brain's own path — feeding the pull and emitting the activity
	// toast (08 §4). Satisfied by *board.Service.
	MarkReady(ctx context.Context, id board.TicketID) (board.Ticket, error)
}

// NotificationPoster is the DEV-ONLY port behind POST /api/dev/notifications:
// post a brain-authored feed notification (update/preview) without the LLM's
// discretion, so an e2e can deterministically produce an update/preview card
// (08 §E.3). Satisfied by *runtime.Service's PostNotification. Mounted only when
// EnableDevNotifications was called; NOT part of the wire contract (/schema).
type NotificationPoster interface {
	PostNotification(ctx context.Context, kind, body string, ticketID, imageURL *string) error
}

// Resetter is the port behind POST /api/dev/reset: return the whole system to a
// fresh agent session — wipe the state tables and tear down the live agent
// sandboxes (docs/superpowers/specs/2026-07-04-debug-reset-session-design.md).
// A developer/debug affordance driven from the /debug client's "Reset session"
// button; NOT part of the wire contract (/schema). Satisfied by the composition
// root's reset coordinator, which spans the DB and the agent service.
type Resetter interface {
	Reset(ctx context.Context) error
}

// VoiceTokenMinter is the api's port onto the STT provider's temporary-token
// mint (09 §6): a short-lived AssemblyAI streaming token the client uses to
// open the STT socket directly, so the real API key never leaves the backend
// (09 §2, 02 §2). One method, so tests fake it trivially and a provider swap
// touches one adapter. Satisfied by *voice/assemblyai.Client.
type VoiceTokenMinter interface {
	MintStreamingToken(ctx context.Context) (token string, expiresAt time.Time, err error)
}

// PushSubscription is one browser Web Push registration as the registrar
// receives it (02 §10) — the api's own shape, so this package never imports
// internal/push (same boundary rule the other ports follow). A cmd/kiln adapter
// maps it to push.Subscription.
type PushSubscription struct {
	Endpoint string
	P256dh   string
	Auth     string
}

// PushRegistrar is the api's port onto the push store (02 §10): the subscription
// write side — POST /api/push/subscribe lands a browser subscription that the
// runtime's notify.send executor later delivers to — plus the global
// notification-frequency mode read/written by GET/PUT /api/push/mode. Mode is a
// plain string on this boundary (the api never imports internal/push); the
// allowed values are validated against the wire enum before SetMode. Satisfied
// by a cmd/kiln adapter over the push store.
type PushRegistrar interface {
	Subscribe(ctx context.Context, sub PushSubscription) error
	Mode(ctx context.Context) (string, error)
	SetMode(ctx context.Context, mode string) error
}

// Authenticator is the api's port onto the GitHub OAuth + cookie-session
// lifecycle (11 §2): start the dance, complete it against the allowlist,
// mint/resolve/revoke a session. Satisfied directly by *identity.Service —
// no adapter — mirroring how BoardReader etc. are satisfied directly by
// their domain services.
type Authenticator interface {
	LoginURL(state string) string
	CompleteLogin(ctx context.Context, code string) (identity.User, error)
	CreateSession(ctx context.Context, userID string) (string, time.Time, error)
	// ResolveSession returns the session's current expiry alongside the user
	// (the renewed one when the sliding window fired, else the existing one)
	// so withSession can re-issue the cookie to match (11 §2 final review).
	ResolveSession(ctx context.Context, token string) (identity.User, time.Time, error)
	Logout(ctx context.Context, token string) error
}

// AccountService is the api's port onto the signed-in account surface (11
// §4): the config-status view, partial credential/project writes, and the
// live connection checks behind GET /api/me, PUT /api/settings, PUT
// /api/project, and POST /api/settings/verify. Satisfied directly by
// *identity.Service, mirroring Authenticator.
type AccountService interface {
	Me(ctx context.Context, userID string) (identity.Me, error)
	UpdateSettings(ctx context.Context, userID string, upd identity.SettingsUpdate) error
	UpsertProject(ctx context.Context, userID string, upd identity.ProjectUpdate) (identity.Project, error)
	Verify(ctx context.Context, userID string) ([]identity.CheckResult, error)
}

// DevSessionMinter is the DEV-ONLY port behind POST /api/dev/session: sign in
// (or create) a user straight from a GitHub login and mint a session for it,
// bypassing the real OAuth dance (11 §7) — so an e2e can establish an
// authenticated session deterministically. Satisfied by *identity.Service.
// Mounted only when EnableDevSession was called; NOT part of the wire
// contract (/schema) — never the real client.
type DevSessionMinter interface {
	DevSignIn(ctx context.Context, login string) (identity.User, error)
	CreateSession(ctx context.Context, userID string) (string, time.Time, error)
}

// Server owns the 04 §7 / 07 §4 endpoint set:
//
//	GET  /api/stream   — SSE: full board snapshot on connect, then one board
//	                     event per board.updated entry; say events carry the
//	                     brain's text replies (07 §4; 09 adds TTS on top);
//	                     comment-line keepalive every 25 s.
//	GET  /api/board    — the same full snapshot for initial render or manual resync.
//	POST /api/message  — user text {text} → transactional transcript append +
//	                     enqueue(human.message) → 202 {event_id, message_id}
//	                     (07 §3–§4; 09 puts STT in front of this seam).
//	GET  /api/messages — most-recent transcript rows, oldest-first (07 §4);
//	                     query param limit, default 50 (schema/openapi.yaml).
//
// Push registration arrives with the notification spec (02 §10); voice with 09.
type Server struct {
	boards   BoardReader
	poster   MessagePoster
	messages MessagesReader
	feed     FeedReader
	seen     FeedMutator
	hub      *Hub
	voice    VoiceTokenMinter
	push     PushRegistrar      // non-nil ⇒ POST /api/push/subscribe + GET /api/push/key are mounted (02 §10)
	vapidKey string             // VAPID public key served by GET /api/push/key; empty ⇒ that route 404s (push disabled)
	seeder   TicketSeeder       // dev-only; non-nil ⇒ POST /api/dev/tickets is mounted
	devNotes NotificationPoster // dev-only; non-nil ⇒ POST /api/dev/notifications is mounted
	resetter Resetter           // non-nil ⇒ POST /api/dev/reset is mounted
	auth     Authenticator      // non-nil ⇒ the /auth/github/* + /auth/logout routes are mounted (11 §2)
	account  AccountService     // the signed-in account surface (11 §4); set together with auth

	devSession DevSessionMinter // dev-only; non-nil (AND auth enabled) ⇒ POST /api/dev/session is mounted (11 §7)

	version    string                      // release string surfaced by GET /healthz
	healthPing func(context.Context) error // non-nil ⇒ GET /healthz is mounted
	spa        http.Handler                // non-nil ⇒ the "/" catch-all serves the embedded SPA
}

// NewServer wires the routes over their ports and the hub.
func NewServer(
	boards BoardReader, poster MessagePoster, messages MessagesReader,
	feed FeedReader, seen FeedMutator, hub *Hub, voice VoiceTokenMinter,
) *Server {
	return &Server{
		boards: boards, poster: poster, messages: messages,
		feed: feed, seen: seen, hub: hub, voice: voice,
	}
}

// EnableDevTickets turns on the dev-only POST /api/dev/tickets route (call before
// Handler). Local/e2e only — gated at the composition root by KILN_DEV_ENDPOINTS.
func (s *Server) EnableDevTickets(seeder TicketSeeder) { s.seeder = seeder }

// EnableDevNotifications turns on the dev-only POST /api/dev/notifications route
// (call before Handler). Local/e2e only — gated by KILN_DEV_ENDPOINTS.
func (s *Server) EnableDevNotifications(poster NotificationPoster) { s.devNotes = poster }

// EnableReset turns on POST /api/dev/reset (call before Handler). Unlike the
// dev seed routes it is wired unconditionally at the composition root — the
// /debug "Reset session" button relies on it always being present.
func (s *Server) EnableReset(r Resetter) { s.resetter = r }

// EnablePush turns on the Web Push registration routes (call before Handler):
// POST /api/push/subscribe stores a browser subscription; GET /api/push/key
// serves the VAPID public key. The registrar (subscription store) is always
// available, so subscribe is always mounted; vapidPublicKey is empty when the
// operator has not configured a VAPID key pair, in which case the key route
// reports 404 and the client hides the notifications toggle (02 §10).
func (s *Server) EnablePush(registrar PushRegistrar, vapidPublicKey string) {
	s.push = registrar
	s.vapidKey = vapidPublicKey
}

// EnableIdentity turns on the GitHub OAuth + cookie-session routes (11 §2):
// GET /auth/github/login, GET /auth/github/callback, POST /auth/logout —
// plus the signed-in account surface (11 §4): GET /api/me, PUT /api/settings,
// PUT /api/project, POST /api/settings/verify (call before Handler). The
// auth routes mount outside /api, ahead of the SPA catch-all; the account
// routes are session-protected /api/* endpoints.
func (s *Server) EnableIdentity(auth Authenticator, account AccountService) {
	s.auth = auth
	s.account = account
}

// EnableDevSession turns on the dev-only POST /api/dev/session route (call
// before Handler, alongside EnableIdentity): mint a session for a
// dev-supplied GitHub login without the real OAuth dance (11 §7). Local/e2e
// only — gated at the composition root by KILN_DEV_ENDPOINTS.
func (s *Server) EnableDevSession(m DevSessionMinter) { s.devSession = m }

// EnableHealthz turns on GET /healthz (call before Handler): a liveness+DB probe
// returning 200 {status:ok, version} when ping succeeds, 503 {status:degraded}
// otherwise. version is the release string; ping is the composition root's DB
// health check (db.PingContext). Mounted outside /api so Render and a first curl
// hit it without the app prefix.
func (s *Server) EnableHealthz(version string, ping func(context.Context) error) {
	s.version = version
	s.healthPing = ping
}

// EnableSPA mounts the embedded frontend as the mux's "/" catch-all (call before
// Handler): every path not claimed by an /api/* or /healthz pattern falls to it,
// so the client's own routes render same-origin with the API.
func (s *Server) EnableSPA(h http.Handler) { s.spa = h }

// Handler returns the api's http.Handler, ready to mount.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/stream", s.handleStream)
	mux.HandleFunc("GET /api/board", s.handleBoard)
	mux.HandleFunc("POST /api/message", s.handleMessage)
	mux.HandleFunc("GET /api/messages", s.handleMessages)
	mux.HandleFunc("GET /api/feed", s.handleFeed)
	mux.HandleFunc("GET /api/feed/history", s.handleFeedHistory)
	mux.HandleFunc("POST /api/feed/seen", s.handleFeedSeen)
	mux.HandleFunc("POST /api/feed/dismiss-all", s.handleFeedDismissAll)
	mux.HandleFunc("POST /api/feed/{id}/dismiss", s.handleFeedDismiss)
	mux.HandleFunc("POST /api/tickets/{id}/accept", s.handleAccept)
	mux.HandleFunc("POST /api/voice/token", s.handleVoiceToken)
	if s.push != nil {
		mux.HandleFunc("POST /api/push/subscribe", s.handlePushSubscribe)
		mux.HandleFunc("GET /api/push/key", s.handlePushKey)
		mux.HandleFunc("GET /api/push/mode", s.handlePushModeGet)
		mux.HandleFunc("PUT /api/push/mode", s.handlePushModeSet)
	}
	if s.seeder != nil {
		mux.HandleFunc("POST /api/dev/tickets", s.handleDevCreateTicket)
	}
	if s.devNotes != nil {
		mux.HandleFunc("POST /api/dev/notifications", s.handleDevPostNotification)
	}
	if s.resetter != nil {
		mux.HandleFunc("POST /api/dev/reset", s.handleReset)
	}
	if s.auth != nil {
		mux.HandleFunc("GET /auth/github/login", s.handleAuthLogin)
		mux.HandleFunc("GET /auth/github/callback", s.handleAuthCallback)
		mux.HandleFunc("POST /auth/logout", s.handleLogout)
		mux.HandleFunc("GET /api/me", s.withSession(s.handleMe))
		mux.HandleFunc("PUT /api/settings", s.withSession(s.handlePutSettings))
		mux.HandleFunc("PUT /api/project", s.withSession(s.handlePutProject))
		mux.HandleFunc("POST /api/settings/verify", s.withSession(s.handleVerify))
		// dev-only (KILN_DEV_ENDPOINTS=1 AND identity enabled): mint a session
		// straight from a GitHub login, bypassing the OAuth dance (11 §7).
		if s.devSession != nil {
			mux.HandleFunc("POST /api/dev/session", s.handleDevSession)
		}
	}
	if s.healthPing != nil {
		mux.HandleFunc("GET /healthz", s.handleHealthz)
	}
	if s.spa != nil {
		// The "/" pattern is the lowest-precedence match in a Go 1.22 ServeMux,
		// so every /api/* and /healthz route above wins first; only unmatched
		// paths (client routes, embedded assets) reach the SPA handler.
		mux.Handle("/", s.spa)
	}
	return mux
}

// handleHealthz is the liveness + DB-reachability probe (design 2026-07-05):
// 200 {status:ok, version} when the DB ping answers, 503 {status:degraded,
// version} otherwise. Mounted outside /api. The ping is bounded by a short
// timeout so a wedged DB fails the check promptly rather than hanging Render's
// probe.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), healthPingTimeout)
	defer cancel()
	if err := s.healthPing(ctx); err != nil {
		slog.Warn("api: healthz degraded", "err", err)
		writeJSON(w, http.StatusServiceUnavailable,
			map[string]string{"status": "degraded", "version": s.version})
		return
	}
	writeJSON(w, http.StatusOK, wire.Health{Status: wire.HealthStatusOk, Version: s.version})
}

// handleReset returns the system to a fresh agent session (204). The reset is
// destructive and irreversible; the /debug client guards it behind a confirm
// dialog. No request body.
func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	if err := s.resetter.Reset(r.Context()); err != nil {
		slog.Error("api: reset", "err", err)
		http.Error(w, "reset", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDevCreateTicket seeds a ticket directly into a target state (dev only),
// so an e2e can establish a feed/board precondition deterministically without
// the brain: the default (no state) is shaping, state=blocked binds a free
// worker and sets a blocked_reason (a blocker card), state=shaping with
// approval_requested surfaces a proposal card, state=ready feeds the pull. Body
// is the work order the agent receives. Mounted only when EnableDevTickets was
// called (KILN_DEV_ENDPOINTS).
func (s *Server) handleDevCreateTicket(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title             string `json:"title"`
		Body              string `json:"body"`
		State             string `json:"state"`
		BlockedReason     string `json:"blocked_reason"`
		ApprovalRequested bool   `json:"approval_requested"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	// A ready seed can't be a direct insert: it would emit no pull.evaluate and
	// no queued toast, leaving the ticket inert. Seed it as shaping, then run the
	// real MarkReady — which feeds the pull and emits the queued activity toast,
	// exactly as the brain's mark_ready would (08 §4).
	seedState := board.State(req.State)
	markReady := seedState == board.StateReady
	if markReady {
		seedState = board.StateShaping
	}
	t, err := s.seeder.SeedTicket(r.Context(), board.SeedSpec{
		Title:             req.Title,
		Body:              req.Body,
		State:             seedState,
		BlockedReason:     req.BlockedReason,
		ApprovalRequested: req.ApprovalRequested,
	})
	if err == nil && markReady {
		t, err = s.seeder.MarkReady(r.Context(), t.ID)
	}
	switch {
	case errors.Is(err, board.ErrNoFreeWorker):
		http.Error(w, "no free worker to bind for a blocked/working seed", http.StatusConflict)
		return
	case err != nil:
		slog.Error("api: dev seed ticket", "err", err)
		http.Error(w, "seed ticket", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": string(t.ID), "state": string(t.State)})
}

// handleDevPostNotification posts a brain-authored feed notification directly
// (dev only), so an e2e can produce an update/preview card without the LLM.
// Mounted only when EnableDevNotifications was called (KILN_DEV_ENDPOINTS).
func (s *Server) handleDevPostNotification(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind     string  `json:"kind"`
		Body     string  `json:"body"`
		TicketID *string `json:"ticket_id"`
		ImageURL *string `json:"image_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := s.devNotes.PostNotification(r.Context(), req.Kind, req.Body, req.TicketID, req.ImageURL); err != nil {
		slog.Error("api: dev post notification", "err", err)
		http.Error(w, "post notification", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

// handleStream serves the SSE connection (04 §7): the hub owns the client
// registry, the snapshot-on-connect, fan-out, and keepalive.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	s.hub.ServeStream(w, r)
}

// handleBoard returns the full board snapshot (04 §7), the same shape the
// stream's board event carries.
func (s *Server) handleBoard(w http.ResponseWriter, r *http.Request) {
	bw, err := s.hub.boardWire(r.Context())
	if err != nil {
		slog.Error("api: get board", "err", err)
		http.Error(w, "read board", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, bw)
}

// handleMessage decodes {text}, validates its bounds (schema MessageRequest),
// delegates to the runtime's transactional PostMessage, and returns 202 with
// both ids (07 §3–§4).
func (s *Server) handleMessage(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxMessageBody)
	var req wire.MessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if len(req.Text) < minMessageLen || len(req.Text) > maxMessageLen {
		http.Error(w, "text must be 1-4000 characters", http.StatusBadRequest)
		return
	}
	messageID, eventID, err := s.poster.PostMessage(r.Context(), req.Text)
	if err != nil {
		slog.Error("api: post message", "err", err)
		http.Error(w, "post message", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusAccepted, wire.MessagePostResponse{MessageId: messageID, EventId: eventID})
}

// handleMessages returns the most-recent transcript rows oldest-first (07 §4),
// honouring the limit query param (default 50, bounds 1–500).
func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	limit := defaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < minLimit || n > maxLimit {
			http.Error(w, "limit must be 1-500", http.StatusBadRequest)
			return
		}
		limit = n
	}
	msgs, err := s.messages.Recent(r.Context(), limit)
	if err != nil {
		slog.Error("api: read messages", "err", err)
		http.Error(w, "read messages", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, messagesToWire(msgs))
}

// handleFeed returns the absolute feed snapshot (08 §3): the same shape the
// stream's feed event carries.
func (s *Server) handleFeed(w http.ResponseWriter, r *http.Request) {
	snap, err := s.feed.Feed(r.Context())
	if err != nil {
		slog.Error("api: read feed", "err", err)
		http.Error(w, "read feed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, feedToWire(snap))
}

// handleFeedHistory returns one older page of retained update/preview history
// (08 D2′): notification-backed cards with id < before, newest-first, plus a
// has_more flag. `before` omitted starts from the newest; `limit` defaults to 30
// (bounds 1–100), mirroring handleMessages.
func (s *Server) handleFeedHistory(w http.ResponseWriter, r *http.Request) {
	before := int64(math.MaxInt64)
	if raw := r.URL.Query().Get("before"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 1 {
			http.Error(w, "before must be a positive notification id", http.StatusBadRequest)
			return
		}
		before = n
	}
	limit := defaultFeedLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < minFeedLimit || n > maxFeedLimit {
			http.Error(w, "limit must be 1-100", http.StatusBadRequest)
			return
		}
		limit = n
	}
	cards, hasMore, err := s.feed.FeedHistory(r.Context(), before, limit)
	if err != nil {
		slog.Error("api: read feed history", "err", err)
		http.Error(w, "read feed history", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, feedHistoryToWire(cards, hasMore))
}

// handleFeedSeen advances the seen high-water mark (08 §3): every update up to
// and including last_notification_id is marked seen. Returns 202.
func (s *Server) handleFeedSeen(w http.ResponseWriter, r *http.Request) {
	var req wire.FeedSeenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := s.seen.MarkSeen(r.Context(), req.LastNotificationId); err != nil {
		slog.Error("api: mark seen", "err", err)
		http.Error(w, "mark seen", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// handleFeedDismiss clears a single update/preview card by swipe (08 §3): the
// user swiped the row left, so retract the notification behind {id} for good.
// Idempotent — an unknown/already-gone id is a no-op under the store's guard.
// Returns 202.
func (s *Server) handleFeedDismiss(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.Error(w, "id must be a positive notification id", http.StatusBadRequest)
		return
	}
	if err := s.seen.DismissNotification(r.Context(), id); err != nil {
		slog.Error("api: dismiss card", "err", err)
		http.Error(w, "dismiss card", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// handleFeedDismissAll clears every feed notification at once (08 §3, clear-all):
// the user tapped the header trash affordance. Retracts all still-active
// notifications for good. Idempotent — a no-op when nothing is active. Returns 202.
func (s *Server) handleFeedDismissAll(w http.ResponseWriter, r *http.Request) {
	if err := s.seen.DismissAllNotifications(r.Context()); err != nil {
		slog.Error("api: dismiss all cards", "err", err)
		http.Error(w, "dismiss all cards", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// handleAccept routes a tap-Accept on a proposal through the brain (08
// implementation-contract decision, overriding 08 D6): look up the ticket's
// title, synthesize an explicit acceptance message, and post it exactly like
// POST /api/message so the brain marks the ticket ready via mark_ready. Returns
// 202 {event_id, message_id} — the same reconcile ids as a normal send.
func (s *Server) handleAccept(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	title := id // fall back to the id if the title lookup fails or misses.
	if snap, err := s.boards.GetBoard(r.Context()); err == nil {
		if t, ok := findTicket(snap, id); ok {
			title = t.Title
		}
	}
	text := fmt.Sprintf(
		"The user tapped Accept on the proposal %q (ticket %s). "+
			"Mark that ticket ready now; do not ask for confirmation.",
		title, id,
	)
	messageID, eventID, err := s.poster.PostMessage(r.Context(), text)
	if err != nil {
		slog.Error("api: accept proposal", "err", err)
		http.Error(w, "accept proposal", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusAccepted, wire.MessagePostResponse{MessageId: messageID, EventId: eventID})
}

// handleVoiceToken mints a short-lived AssemblyAI streaming token (09 §6) and
// returns it with its absolute expiry. The client opens the STT WebSocket
// directly with this token; audio never transits our backend (09 §2). A
// provider mint failure is a 502 — the client's one silent reconnect then
// Retry surface handles it (09 §5).
func (s *Server) handleVoiceToken(w http.ResponseWriter, r *http.Request) {
	token, expiresAt, err := s.voice.MintStreamingToken(r.Context())
	if err != nil {
		slog.Error("api: mint voice token", "err", err)
		http.Error(w, "mint voice token", http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, wire.VoiceToken{Token: token, ExpiresAt: expiresAt})
}

// handlePushKey serves the VAPID public key for pushManager.subscribe (02 §10).
// When no key is configured (the VAPID env vars are unset) it 404s, and the
// client treats notifications as unavailable.
func (s *Server) handlePushKey(w http.ResponseWriter, _ *http.Request) {
	if s.vapidKey == "" {
		http.Error(w, "push not configured", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, wire.PushKey{Key: s.vapidKey})
}

// handlePushSubscribe stores a browser PushSubscription (02 §10). Upsert on
// endpoint (via the registrar), so a re-subscribe is idempotent; 204 on success.
func (s *Server) handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxPushBody)
	var req wire.PushSubscription
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Endpoint == "" || req.Keys.P256dh == "" || req.Keys.Auth == "" {
		http.Error(w, "endpoint and keys are required", http.StatusBadRequest)
		return
	}
	if err := s.push.Subscribe(r.Context(), PushSubscription{
		Endpoint: req.Endpoint,
		P256dh:   req.Keys.P256dh,
		Auth:     req.Keys.Auth,
	}); err != nil {
		slog.Error("api: store push subscription", "err", err)
		http.Error(w, "store subscription", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handlePushModeGet returns the current notification frequency (02 §10) — the
// single global mode gating when the runtime emits a Web Push message.
func (s *Server) handlePushModeGet(w http.ResponseWriter, r *http.Request) {
	mode, err := s.push.Mode(r.Context())
	if err != nil {
		slog.Error("api: read push mode", "err", err)
		http.Error(w, "read mode", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, wire.NotificationMode{Mode: wire.NotificationModeMode(mode)})
}

// handlePushModeSet persists the notification frequency (02 §10). The mode must
// be one of the wire enum values (validated via NotificationModeMode.Valid), so
// an unknown mode is a 400 rather than a silent write. Echoes the stored mode.
func (s *Server) handlePushModeSet(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxPushBody)
	var req wire.NotificationMode
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if !req.Mode.Valid() {
		http.Error(w, "mode must be one of: all, blocked", http.StatusBadRequest)
		return
	}
	if err := s.push.SetMode(r.Context(), string(req.Mode)); err != nil {
		slog.Error("api: set push mode", "err", err)
		http.Error(w, "set mode", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, wire.NotificationMode{Mode: req.Mode})
}

// findTicket locates a ticket by id across every group of a board snapshot.
func findTicket(snap board.Snapshot, id string) (board.Ticket, bool) {
	groups := [][]board.Ticket{snap.Shaping, snap.Ready, snap.Blocked, snap.Working, snap.Done}
	for _, g := range groups {
		for _, t := range g {
			if string(t.ID) == id {
				return t, true
			}
		}
	}
	return board.Ticket{}, false
}

// writeJSON encodes v as the response body with the given status. An encode
// failure is logged, not returned — the header is already committed.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("api: encode response", "err", err)
	}
}

// boardToWire maps a board.Snapshot plus the joined live-worker statuses onto
// the generated wire.Board (04 D7): the identical shape backs GET /api/board
// and the board SSE event. agents is the Streams view's real session status,
// joined server-side (amended 2026-07-05).
func boardToWire(s board.Snapshot, agents []wire.AgentStatus) wire.Board {
	return wire.Board{
		Shaping:     ticketsToWire(s.Shaping),
		Ready:       ticketsToWire(s.Ready),
		Blocked:     ticketsToWire(s.Blocked),
		Working:     ticketsToWire(s.Working),
		Done:        ticketsToWire(s.Done),
		WorkerTotal: s.WorkerTotal,
		WorkerFree:  s.WorkerFree,
		Agents:      agents,
	}
}

// agentStatuses reads the live-worker statuses and maps them to the wire shape,
// always returning a non-nil slice so the JSON is an array (never null). It is
// best-effort: a nil inspector or a read failure yields an empty array — the
// board still renders, Streams just shows nothing new (amended 2026-07-05).
func agentStatuses(ctx context.Context, inspector AgentInspector) []wire.AgentStatus {
	if inspector == nil {
		return []wire.AgentStatus{}
	}
	infos, err := inspector.ListAgents(ctx)
	if err != nil {
		slog.WarnContext(ctx, "api: list agents for board", "err", err)
		return []wire.AgentStatus{}
	}
	out := make([]wire.AgentStatus, 0, len(infos))
	for _, a := range infos {
		out = append(out, wire.AgentStatus{
			WorkerId: a.WorkerID,
			TicketId: a.TicketID,
			Status:   wire.AgentStatusStatus(a.Status),
		})
	}
	return out
}

// ticketsToWire maps a ticket group, always returning a non-nil slice so the
// JSON is an array (never null) — the client renders columns of arrays.
func ticketsToWire(ts []board.Ticket) []wire.Ticket {
	out := make([]wire.Ticket, 0, len(ts))
	for _, t := range ts {
		out = append(out, ticketToWire(t))
	}
	return out
}

func ticketToWire(t board.Ticket) wire.Ticket {
	return wire.Ticket{
		Id:                string(t.ID),
		Title:             t.Title,
		Body:              t.Body,
		State:             wire.TicketState(t.State),
		Priority:          t.Priority,
		BlockedReason:     t.BlockedReason,
		ReadyAt:           t.ReadyAt,
		ApprovalRequested: t.ApprovalRequested,
		CreatedAt:         t.CreatedAt,
		UpdatedAt:         t.UpdatedAt,
		StateChangedAt:    t.StateChangedAt,
	}
}

// feedToWire maps a runtime.FeedSnapshot onto the generated wire.FeedSnapshot
// (08 §3, D2′): the identical absolute shape backs GET /api/feed and the feed SSE
// event. Nullable card fields (TicketId, ImageUrl, NotificationId) and the
// summary's LastWordAt / LastSeenNotificationId carry through as pointers
// untouched; HasMoreHistory signals older retained updates page in via
// /api/feed/history.
func feedToWire(s runtime.FeedSnapshot) wire.FeedSnapshot {
	return wire.FeedSnapshot{
		Summary: wire.FeedSummary{
			BlockerCount:           s.Summary.BlockerCount,
			UpdateCount:            s.Summary.UpdateCount,
			StreamCount:            s.Summary.StreamCount,
			Building:               s.Summary.Building,
			Idle:                   s.Summary.Idle,
			LastWordAt:             s.Summary.LastWordAt,
			LastSeenNotificationId: s.Summary.LastSeenNotificationID,
		},
		Cards:          feedCardsToWire(s.Cards),
		HasMoreHistory: s.HasMoreHistory,
	}
}

// feedHistoryToWire maps one older page of update/preview cards onto the
// generated wire.FeedHistoryPage (08 D2′ GET /api/feed/history).
func feedHistoryToWire(cards []runtime.FeedCard, hasMore bool) wire.FeedHistoryPage {
	return wire.FeedHistoryPage{Cards: feedCardsToWire(cards), HasMore: hasMore}
}

// feedCardsToWire maps runtime feed cards onto wire.FeedCard, always non-nil.
// Shared by the snapshot and the history page.
func feedCardsToWire(in []runtime.FeedCard) []wire.FeedCard {
	cards := make([]wire.FeedCard, 0, len(in))
	for _, c := range in {
		cards = append(cards, wire.FeedCard{
			Kind:           wire.FeedCardKind(c.Kind),
			Id:             c.ID,
			Label:          c.Label,
			Body:           c.Body,
			TicketId:       c.TicketID,
			ImageUrl:       c.ImageURL,
			NotificationId: c.NotificationID,
			CreatedAt:      c.CreatedAt,
		})
	}
	return cards
}

// activityToWire maps a runtime.ActivityEvent onto the generated
// wire.ActivityEvent (08 §4). The wire type keys its optional fields by kind:
// On is set only for thinking; Verb and TicketTitle only for a toast (and only
// when non-empty), left nil otherwise.
func activityToWire(ev runtime.ActivityEvent) wire.ActivityEvent {
	out := wire.ActivityEvent{Kind: wire.ActivityEventKind(ev.Kind)}
	if ev.Kind == string(wire.Thinking) {
		out.On = ev.On
	}
	if ev.Verb != "" {
		v := wire.ActivityEventVerb(ev.Verb)
		out.Verb = &v
	}
	if ev.TicketTitle != "" {
		tt := ev.TicketTitle
		out.TicketTitle = &tt
	}
	return out
}

// messagesToWire maps transcript rows onto wire.Message, always non-nil.
func messagesToWire(ms []runtime.Message) []wire.Message {
	out := make([]wire.Message, 0, len(ms))
	for _, m := range ms {
		out = append(out, wire.Message{
			MessageId: m.ID,
			Role:      wire.MessageRole(m.Role),
			Text:      m.Text,
			Timestamp: m.CreatedAt,
		})
	}
	return out
}
