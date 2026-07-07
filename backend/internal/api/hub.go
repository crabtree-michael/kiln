package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/runtime"
	"github.com/crabtree-michael/kiln/backend/internal/wire"
)

// keepaliveInterval is the SSE comment-line heartbeat cadence (04 §7): it
// keeps idle connections and intermediaries from timing out without carrying
// any payload.
const keepaliveInterval = 25 * time.Second

// clientBuffer bounds each connected client's pending-frame queue. A client
// that falls this far behind drops frames rather than blocking the broadcaster
// — safe because board frames are absolute snapshots (04 D7), so the next push
// fully resyncs it; say frames are reconciled by message_id on the next fetch.
const clientBuffer = 16

// SSE event names (04 §7, 07 §4, 08 §3–§4): the named events the client
// listens for.
const (
	eventBoard    = "board"
	eventSay      = "say"
	eventFeed     = "feed"
	eventActivity = "activity"
)

// sseFrame is one named SSE event ready to write: `event: <name>\ndata: <data>\n\n`.
type sseFrame struct {
	event string
	data  []byte
}

// client is one connected SSE stream's outbound queue.
type client struct {
	ch chan sseFrame
}

// Hub tracks connected SSE clients and fans out server→client events
// (04 §7–§8). It implements the runtime's SnapshotPusher port: a
// board.updated outbox entry becomes one absolute GetBoard snapshot pushed
// to every client (03 D7 / 04 D7 — never deltas, so reconnect needs no
// replay and duplicates are harmless). It also implements the runtime's
// SayPusher port (07 §3–§4): a say event ({message_id, text, at}) rides the
// same per-client streams as board events, distinguished by SSE event name.
type Hub struct {
	boards BoardReader
	agents AgentInspector // may be nil; set post-construction via SetAgentInspector

	mu      sync.Mutex
	clients map[*client]struct{}

	// thinking mirrors the last `thinking` bracket that passed through
	// PushActivity — the authoritative current spinner state, since every on/off
	// fans out through here and brain passes run serially (04 §4). The activity
	// event itself is ephemeral (never replayed on reconnect), so this is what
	// GET /api/activity reads to resync a client on foreground/resume (08 §4).
	// Guarded by mu, alongside the client set it is written next to.
	thinking bool
}

// NewHub wires fan-out over the board's read path.
func NewHub(boards BoardReader) *Hub {
	return &Hub{boards: boards, clients: make(map[*client]struct{})}
}

// SetAgentInspector late-binds the live-worker status source joined into every
// board snapshot (amended 2026-07-05). Late because the agent service is built
// after the hub at the composition root, and the hub is what the agent's
// liveness loop nudges to re-push — a construction cycle broken here rather than
// with an empty adapter. Nil (never called) leaves the agents array empty.
func (h *Hub) SetAgentInspector(a AgentInspector) { h.agents = a }

// The hub satisfies every runtime push port: board/say snapshots (04/07) and
// the 08 feed/activity fan-out.
var (
	_ runtime.SnapshotPusher = (*Hub)(nil)
	_ runtime.SayPusher      = (*Hub)(nil)
	_ runtime.FeedPusher     = (*Hub)(nil)
	_ runtime.ActivityPusher = (*Hub)(nil)
)

// ServeStream handles one /api/stream connection (04 §7): send the current
// board snapshot immediately, then stream board/say frames as they are pushed,
// with a comment-line keepalive, until the client disconnects. Reconnect is a
// fresh snapshot, never a replay (04 D6/D7).
func (h *Hub) ServeStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	c := &client{ch: make(chan sseFrame, clientBuffer)}
	h.add(c)
	defer h.remove(c)

	if !h.writeInitialSnapshot(r.Context(), w, flusher) {
		return
	}

	keepalive := time.NewTicker(keepaliveInterval)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case f := <-c.ch:
			if err := writeFrame(w, f); err != nil {
				return
			}
			flusher.Flush()
		case <-keepalive.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// PushBoard implements runtime.SnapshotPusher (04 §2): read one fresh
// snapshot, send it to every connected stream.
func (h *Hub) PushBoard(ctx context.Context) error {
	bw, err := h.boardWire(ctx)
	if err != nil {
		return fmt.Errorf("api: push board: %w", err)
	}
	data, err := json.Marshal(bw)
	if err != nil {
		return fmt.Errorf("api: marshal board event: %w", err)
	}
	h.broadcast(sseFrame{event: eventBoard, data: data})
	return nil
}

// PushSay implements runtime.SayPusher (07 §3–§4): send one say event to
// every connected stream. Duplicates on crash-replay are benign — the
// client reconciles by message_id (07 §5).
func (h *Hub) PushSay(_ context.Context, m runtime.Message) error {
	data, err := json.Marshal(wire.SayEvent{MessageId: m.ID, Text: m.Text, At: m.CreatedAt})
	if err != nil {
		return fmt.Errorf("api: marshal say event: %w", err)
	}
	h.broadcast(sseFrame{event: eventSay, data: data})
	return nil
}

// PushFeed implements runtime.FeedPusher (08 §3): fan one absolute FeedSnapshot
// out to every connected stream, distinguished by the feed event name. The
// snapshot is passed in already assembled — the hub never reads the feed
// itself, so there is no feed-reader dependency (and no runtime↔api cycle).
func (h *Hub) PushFeed(_ context.Context, snap runtime.FeedSnapshot) error {
	data, err := json.Marshal(feedToWire(snap))
	if err != nil {
		return fmt.Errorf("api: marshal feed event: %w", err)
	}
	h.broadcast(sseFrame{event: eventFeed, data: data})
	return nil
}

// PushActivity implements runtime.ActivityPusher (08 §4): fan one ephemeral
// activity event (thinking bracket or toast) out to every connected stream.
// Ephemeral — never stored, never replayed on reconnect.
func (h *Hub) PushActivity(_ context.Context, ev runtime.ActivityEvent) error {
	data, err := json.Marshal(activityToWire(ev))
	if err != nil {
		return fmt.Errorf("api: marshal activity event: %w", err)
	}
	// Record the thinking bracket before fanning it out, so GET /api/activity
	// reflects the state the instant this push lands. Toasts carry no On and
	// leave it untouched.
	if ev.Kind == "thinking" && ev.On != nil {
		h.setThinking(*ev.On)
	}
	h.broadcast(sseFrame{event: eventActivity, data: data})
	return nil
}

// Thinking reports the current spinner state — the last `thinking` bracket
// pushed (08 §4). Read by GET /api/activity so a client that missed the closing
// `on:false` (backgrounded mid-pass) can resync on resume.
func (h *Hub) Thinking() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.thinking
}

func (h *Hub) setThinking(on bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.thinking = on
}

// boardWire reads one fresh snapshot and joins the live-worker statuses onto it
// (amended 2026-07-05) — the single shape behind GET /api/board, the connect
// snapshot, and every board push.
func (h *Hub) boardWire(ctx context.Context) (wire.Board, error) {
	snap, err := h.boards.GetBoard(ctx)
	if err != nil {
		return wire.Board{}, fmt.Errorf("api: get board: %w", err)
	}
	return boardToWire(snap, agentStatuses(ctx, h.agents)), nil
}

// writeInitialSnapshot sends the connect-time board event (04 §7). It returns
// false if the snapshot could not be read or written, so ServeStream can bail.
func (h *Hub) writeInitialSnapshot(ctx context.Context, w io.Writer, flusher http.Flusher) bool {
	bw, err := h.boardWire(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "api: initial snapshot: get board", "err", err)
		return false
	}
	data, err := json.Marshal(bw)
	if err != nil {
		slog.ErrorContext(ctx, "api: initial snapshot: marshal board", "err", err)
		return false
	}
	if err := writeFrame(w, sseFrame{event: eventBoard, data: data}); err != nil {
		slog.ErrorContext(ctx, "api: initial snapshot: write frame", "err", err)
		return false
	}
	flusher.Flush()
	return true
}

func (h *Hub) add(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = struct{}{}
}

func (h *Hub) remove(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c)
}

// broadcast enqueues a frame to every client, non-blocking: a client whose
// buffer is full drops the frame and resyncs on the next absolute snapshot.
func (h *Hub) broadcast(f sseFrame) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		select {
		case c.ch <- f:
		default:
		}
	}
}

// writeFrame renders one SSE event. data is compact JSON (single line), so the
// `data:` field never needs multi-line continuation.
func writeFrame(w io.Writer, f sseFrame) error {
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", f.event, f.data); err != nil {
		return fmt.Errorf("api: write sse frame: %w", err)
	}
	return nil
}
