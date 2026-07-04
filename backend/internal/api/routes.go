package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/crabtree-michael/kiln/backend/internal/board"
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

// maxMessageBody caps the POST /api/message request body before it is
// decoded, so a hostile client cannot force the server to buffer an
// arbitrarily large body into memory (the maxMessageLen check only runs after
// a full decode). 64 KiB comfortably admits any valid MessageRequest — a
// 4000-char text plus JSON/UTF-8 escaping overhead — while rejecting anything
// larger up front.
const maxMessageBody = 64 << 10

// BoardReader is the api's port onto the board's read path (03 §4 GetBoard).
type BoardReader interface {
	GetBoard(ctx context.Context) (board.Snapshot, error)
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
	hub      *Hub
}

// NewServer wires the routes over their ports and the hub.
func NewServer(boards BoardReader, poster MessagePoster, messages MessagesReader, hub *Hub) *Server {
	return &Server{boards: boards, poster: poster, messages: messages, hub: hub}
}

// Handler returns the api's http.Handler, ready to mount.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/stream", s.handleStream)
	mux.HandleFunc("GET /api/board", s.handleBoard)
	mux.HandleFunc("POST /api/message", s.handleMessage)
	mux.HandleFunc("GET /api/messages", s.handleMessages)
	return mux
}

// handleStream serves the SSE connection (04 §7): the hub owns the client
// registry, the snapshot-on-connect, fan-out, and keepalive.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	s.hub.ServeStream(w, r)
}

// handleBoard returns the full board snapshot (04 §7), the same shape the
// stream's board event carries.
func (s *Server) handleBoard(w http.ResponseWriter, r *http.Request) {
	snap, err := s.boards.GetBoard(r.Context())
	if err != nil {
		slog.Error("api: get board", "err", err)
		http.Error(w, "read board", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, boardToWire(snap))
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

// writeJSON encodes v as the response body with the given status. An encode
// failure is logged, not returned — the header is already committed.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("api: encode response", "err", err)
	}
}

// boardToWire maps a board.Snapshot onto the generated wire.Board (04 D7): the
// identical shape backs GET /api/board and the board SSE event.
func boardToWire(s board.Snapshot) wire.Board {
	return wire.Board{
		Shaping:     ticketsToWire(s.Shaping),
		Ready:       ticketsToWire(s.Ready),
		Blocked:     ticketsToWire(s.Blocked),
		Working:     ticketsToWire(s.Working),
		Done:        ticketsToWire(s.Done),
		WorkerTotal: s.WorkerTotal,
		WorkerFree:  s.WorkerFree,
	}
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
		Id:            string(t.ID),
		Title:         t.Title,
		Body:          t.Body,
		State:         wire.TicketState(t.State),
		Priority:      t.Priority,
		BlockedReason: t.BlockedReason,
		ReadyAt:       t.ReadyAt,
		CreatedAt:     t.CreatedAt,
		UpdatedAt:     t.UpdatedAt,
	}
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
