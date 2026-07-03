package api

import (
	"context"

	"github.com/crabtree-michael/kiln/backend/internal/runtime"
)

// Hub tracks connected SSE clients and fans out server→client events
// (04 §7–§8). It implements the runtime's SnapshotPusher port: a
// board.updated outbox entry becomes one absolute GetBoard snapshot pushed
// to every client (03 D7 / 04 D7 — never deltas, so reconnect needs no
// replay and duplicates are harmless). It also implements the runtime's
// SayPusher port (07 §3–§4): a say event ({message_id, text, at}) rides the
// same per-client streams as board events, distinguished by SSE event name.
type Hub struct {
	boards BoardReader
}

// NewHub wires fan-out over the board's read path.
func NewHub(boards BoardReader) *Hub { return &Hub{boards: boards} }

// PushBoard implements runtime.SnapshotPusher (04 §2): read one fresh
// snapshot, send it to every connected stream.
func (h *Hub) PushBoard(ctx context.Context) error {
	return errNotImplemented
}

// PushSay implements runtime.SayPusher (07 §3–§4): send one say event to
// every connected stream. Duplicates on crash-replay are benign — the
// client reconciles by message_id (07 §5).
func (h *Hub) PushSay(ctx context.Context, m runtime.Message) error {
	return errNotImplemented
}
