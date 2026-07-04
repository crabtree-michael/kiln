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

// SSE event names (04 §7, 07 §4): the two named events the client listens for.
const (
	eventBoard = "board"
	eventSay   = "say"
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

	mu      sync.Mutex
	clients map[*client]struct{}
}

// NewHub wires fan-out over the board's read path.
func NewHub(boards BoardReader) *Hub {
	return &Hub{boards: boards, clients: make(map[*client]struct{})}
}

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
	snap, err := h.boards.GetBoard(ctx)
	if err != nil {
		return fmt.Errorf("api: push board: %w", err)
	}
	data, err := json.Marshal(boardToWire(snap))
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

// writeInitialSnapshot sends the connect-time board event (04 §7). It returns
// false if the snapshot could not be read or written, so ServeStream can bail.
func (h *Hub) writeInitialSnapshot(ctx context.Context, w io.Writer, flusher http.Flusher) bool {
	snap, err := h.boards.GetBoard(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "api: initial snapshot: get board", "err", err)
		return false
	}
	data, err := json.Marshal(boardToWire(snap))
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
