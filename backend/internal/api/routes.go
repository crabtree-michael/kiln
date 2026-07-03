package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/crabtree-michael/kiln/backend/internal/board"
	"github.com/crabtree-michael/kiln/backend/internal/runtime"
)

// errNotImplemented marks scaffold stubs. Implementations follow
// docs/specs/04-runtime-and-api.md; remove this once the last stub is gone.
var errNotImplemented = errors.New("api: not implemented (scaffold)")

// BoardReader is the api's port onto the board's read path (03 §4 GetBoard).
type BoardReader interface {
	GetBoard(ctx context.Context) (board.Snapshot, error)
}

// EventEnqueuer is the api's port onto runtime ingestion (04 §6).
type EventEnqueuer interface {
	EnqueueEvent(ctx context.Context, t runtime.EventType, payload []byte) (int64, error)
}

// Server owns the 04 §7 endpoint set:
//
//	GET  /api/stream   — SSE: full board snapshot on connect, then one board
//	                     event per board.updated entry; say events carry the
//	                     brain's text replies (07 §4; 09 adds TTS on top);
//	                     comment-line keepalive every 25 s.
//	GET  /api/board    — the same full snapshot for initial render or manual resync.
//	POST /api/message  — user text {text} → transactional transcript append +
//	                     EnqueueEvent(human.message) → 202 {event_id, message_id}
//	                     (07 §3–§4; 09 puts STT in front of this seam).
//	GET  /api/messages — most-recent transcript rows, oldest-first (07 §4).
//
// Push registration arrives with the notification spec (02 §10); voice with 09.
type Server struct {
	boards BoardReader
	events EventEnqueuer
	hub    *Hub
}

// NewServer wires the routes over their ports and the hub.
func NewServer(boards BoardReader, events EventEnqueuer, hub *Hub) *Server {
	return &Server{boards: boards, events: events, hub: hub}
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

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	http.Error(w, errNotImplemented.Error(), http.StatusNotImplemented)
}

func (s *Server) handleBoard(w http.ResponseWriter, r *http.Request) {
	http.Error(w, errNotImplemented.Error(), http.StatusNotImplemented)
}

func (s *Server) handleMessage(w http.ResponseWriter, r *http.Request) {
	http.Error(w, errNotImplemented.Error(), http.StatusNotImplemented)
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	http.Error(w, errNotImplemented.Error(), http.StatusNotImplemented)
}
