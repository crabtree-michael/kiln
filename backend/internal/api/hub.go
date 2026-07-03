package api

import "context"

// Hub tracks connected SSE clients and fans out server→client events
// (04 §7–§8). It implements the runtime's SnapshotPusher port: a
// board.updated outbox entry becomes one absolute GetBoard snapshot pushed
// to every client (03 D7 / 04 D7 — never deltas, so reconnect needs no
// replay and duplicates are harmless). say events ride the same streams
// (payload shape: 07 §4).
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
