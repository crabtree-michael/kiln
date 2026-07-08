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

// activityKindThinking is the ActivityEvent.Kind that carries the brain-pass
// spinner bracket (08 §4) — the one activity kind whose On flag the hub records
// per project for GET /api/activity resync.
const activityKindThinking = "thinking"

// sseFrame is one named SSE event ready to write: `event: <name>\ndata: <data>\n\n`.
type sseFrame struct {
	event string
	data  []byte
}

// client is one connected SSE stream's outbound queue, tagged with the project
// it is scoped to (11 phase 2): broadcast only enqueues a frame to clients whose
// projectID matches the push's, so one project's board/say/feed/activity events
// never leak onto another project's stream.
type client struct {
	ch        chan sseFrame
	projectID string
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

	// thinking mirrors, per project, the last `thinking` bracket that passed
	// through PushActivity for that project — the authoritative current spinner
	// state, since every on/off fans out through here. Keyed by projectID so one
	// tenant's "brain thinking" state is never visible to another (11 phase 2):
	// the flag was hub-global before, leaking project A's spinner onto project B.
	// The activity event itself is ephemeral (never replayed on reconnect), so
	// this map is what GET /api/activity reads to resync a client on
	// foreground/resume (08 §4). Guarded by mu, alongside the client set it is
	// written next to. An absent key reads as false (not thinking).
	thinking map[string]bool
}

// NewHub wires fan-out over the board's read path.
func NewHub(boards BoardReader) *Hub {
	return &Hub{boards: boards, clients: make(map[*client]struct{}), thinking: make(map[string]bool)}
}

// SetAgentInspector late-binds the live-worker status source joined into every
// board snapshot (amended 2026-07-05). Late because the agent service is built
// after the hub at the composition root, and the hub is what the agent's
// liveness loop nudges to re-push — a construction cycle broken here rather than
// with an empty adapter. Nil (never called) leaves the agents array empty.
func (h *Hub) SetAgentInspector(a AgentInspector) { h.agents = a }

// The hub satisfies every runtime push port: board/say snapshots (04/07) and
// the 08 feed/activity fan-out. Each port method now carries the projectID the
// event belongs to (11 phase 2), so the fan-out stays scoped to that project's
// streams. The compile-time interface assertions live at the composition root
// (cmd/kiln), which pairs this Hub with the runtime whose ports match this
// tenancy-aware shape — asserting them here would couple the api package's tests
// to that cross-module change.

// ServeStream handles one /api/stream connection (04 §7): send the current
// board snapshot immediately, then stream board/say frames as they are pushed,
// with a comment-line keepalive, until the client disconnects. Reconnect is a
// fresh snapshot, never a replay (04 D6/D7).
func (h *Hub) ServeStream(w http.ResponseWriter, r *http.Request, projectID string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	c := &client{ch: make(chan sseFrame, clientBuffer), projectID: projectID}
	h.add(c)
	defer h.remove(c)

	if !h.writeInitialSnapshot(r.Context(), projectID, w, flusher) {
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
func (h *Hub) PushBoard(ctx context.Context, projectID string) error {
	bw, err := h.boardWire(ctx, projectID)
	if err != nil {
		return fmt.Errorf("api: push board: %w", err)
	}
	data, err := json.Marshal(bw)
	if err != nil {
		return fmt.Errorf("api: marshal board event: %w", err)
	}
	h.broadcast(projectID, sseFrame{event: eventBoard, data: data})
	return nil
}

// PushSay implements runtime.SayPusher (07 §3–§4): send one say event to
// every connected stream. Duplicates on crash-replay are benign — the
// client reconciles by message_id (07 §5).
func (h *Hub) PushSay(_ context.Context, projectID string, m runtime.Message) error {
	data, err := json.Marshal(wire.SayEvent{MessageId: m.ID, Text: m.Text, At: m.CreatedAt})
	if err != nil {
		return fmt.Errorf("api: marshal say event: %w", err)
	}
	h.broadcast(projectID, sseFrame{event: eventSay, data: data})
	return nil
}

// PushFeed implements runtime.FeedPusher (08 §3): fan one absolute FeedSnapshot
// out to every connected stream, distinguished by the feed event name. The
// snapshot is passed in already assembled — the hub never reads the feed
// itself, so there is no feed-reader dependency (and no runtime↔api cycle).
func (h *Hub) PushFeed(_ context.Context, projectID string, snap runtime.FeedSnapshot) error {
	data, err := json.Marshal(feedToWire(snap))
	if err != nil {
		return fmt.Errorf("api: marshal feed event: %w", err)
	}
	h.broadcast(projectID, sseFrame{event: eventFeed, data: data})
	return nil
}

// PushActivity implements runtime.ActivityPusher (08 §4): fan one ephemeral
// activity event (thinking bracket or toast) out to every connected stream.
// Ephemeral — never stored, never replayed on reconnect.
func (h *Hub) PushActivity(_ context.Context, projectID string, ev runtime.ActivityEvent) error {
	data, err := json.Marshal(activityToWire(ev))
	if err != nil {
		return fmt.Errorf("api: marshal activity event: %w", err)
	}
	// Record the thinking bracket before fanning it out, so GET /api/activity
	// reflects the state the instant this push lands. Scoped to this project's
	// entry (11 phase 2). Toasts carry no On and leave it untouched.
	if ev.Kind == activityKindThinking && ev.On != nil {
		h.setThinking(projectID, *ev.On)
	}
	h.broadcast(projectID, sseFrame{event: eventActivity, data: data})
	return nil
}

// Thinking reports the current spinner state for one project — the last
// `thinking` bracket pushed for it (08 §4, 11 phase 2). Read by GET /api/activity
// (scoped to the caller's resolved project) so a client that missed the closing
// `on:false` (backgrounded mid-pass) can resync on resume without ever observing
// another tenant's brain state. An unbracketed project reads false.
func (h *Hub) Thinking(projectID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.thinking[projectID]
}

func (h *Hub) setThinking(projectID string, on bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.thinking[projectID] = on
}

// boardWire reads one fresh snapshot and joins the live-worker statuses onto it
// (amended 2026-07-05) — the single shape behind GET /api/board, the connect
// snapshot, and every board push.
func (h *Hub) boardWire(ctx context.Context, projectID string) (wire.Board, error) {
	snap, err := h.boards.GetBoard(ctx, projectID)
	if err != nil {
		return wire.Board{}, fmt.Errorf("api: get board: %w", err)
	}
	return boardToWire(snap, agentStatuses(ctx, projectID, h.agents)), nil
}

// writeInitialSnapshot sends the connect-time board event (04 §7). It returns
// false if the snapshot could not be read or written, so ServeStream can bail.
func (h *Hub) writeInitialSnapshot(ctx context.Context, projectID string, w io.Writer, flusher http.Flusher) bool {
	bw, err := h.boardWire(ctx, projectID)
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

// broadcast enqueues a frame to every client scoped to projectID, non-blocking:
// a client whose buffer is full drops the frame and resyncs on the next absolute
// snapshot. Clients of other projects never see the frame (11 phase 2) — the
// fan-out is partitioned by project so board/say/feed/activity events stay
// within the tenant they belong to.
func (h *Hub) broadcast(projectID string, f sseFrame) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		if c.projectID != projectID {
			continue
		}
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
