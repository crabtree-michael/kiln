package main

// Port adapters — the composition root's core job (02 §2, 04 §8 D9): every
// internal/* module depends only on the narrow ports it declares, never on a
// sibling module's concrete type, so this is the only file where board.Service,
// brain.Service, agent.Service, and runtime.Service meet. Two kinds of seam
// show up below:
//
//   - Direct satisfaction: some ports match a service's method set exactly
//     (same param/return types, only primitives or shared named types
//     involved), so the concrete *xxx.Service is injected with no adapter at
//     all. Already asserted at the producing end: *board.Service satisfies
//     brain.BoardAPI/BoardReader (internal/brain/ports.go) and
//     runtime.Puller (RunPull); *agent.Service satisfies runtime.AgentRuntime
//     (Send/Release); *api.Hub satisfies runtime.SnapshotPusher/SayPusher;
//     *runtime.Service satisfies api.MessagePoster/MessagesReader and
//     brain.Say.
//   - Adapted satisfaction: the port and the service disagree on a type even
//     though the operation is the same — an outer module's own named Event
//     type vs. runtime's, board's typed (Ticket, error) return vs. runtime's
//     bare error, a plain string vs. a named EventType. Each such pair gets a
//     one-method wrapper below. Bodies are scaffold stubs (errNotImplemented)
//     — the real conversions are the solution phase's job — but every
//     wrapper's signature is asserted at compile time against the port it
//     fills, so the wiring shape itself is already checked by `go build`.
//
// See the package doc (main.go) for the one open composition problem these
// adapters don't solve yet: runtime.Service needs a Brain (backed by
// brain.Service) and brain.Service needs a Say + ConversationReader (backed
// by runtime.Service) — a genuine construction cycle, left for the solution
// phase.

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
	"github.com/crabtree-michael/kiln/backend/internal/board"
	"github.com/crabtree-michael/kiln/backend/internal/brain"
	"github.com/crabtree-michael/kiln/backend/internal/runtime"
)

// errNotImplemented marks scaffold stubs in this package; see
// docs/specs/04-runtime-and-api.md §8 (the composition root).
var errNotImplemented = errors.New("kiln: not implemented (scaffold)")

// brainAdapter satisfies runtime.Brain over *brain.Service (04 §2 Brain
// port). The only mismatch is Event: runtime's own type vs. brain's own type
// (both mirror the same two 01 event kinds by value — neither module imports
// the other, brain/doc.go's no-runtime-import rule). The real body maps
// field-for-field (ID, Type, Payload, CreatedAt) and calls inner.HandleEvent.
type brainAdapter struct{ inner *brain.Service }

func (a *brainAdapter) HandleEvent(ctx context.Context, ev runtime.Event) error {
	return errNotImplemented
}

var _ runtime.Brain = (*brainAdapter)(nil)

// blockerAdapter satisfies runtime.Blocker over *board.Service (04 §2
// Blocker port; 03 §7.3's mechanical failure path). Board's MarkBlocked
// takes a board.TicketID and returns (Ticket, error); the port wants a
// plain-string id and a bare error. The real body converts the id and
// discards the returned Ticket.
type blockerAdapter struct{ inner *board.Service }

func (a *blockerAdapter) MarkBlocked(ctx context.Context, ticketID, reason string) error {
	return errNotImplemented
}

var _ runtime.Blocker = (*blockerAdapter)(nil)

// agentEventAdapter satisfies agent.EventEnqueuer over *runtime.Service
// (05 §2.2's inbound seam). The agent module's port takes a plain string
// eventType (so it need not import runtime — mirroring brain's rule); the
// real body wraps it as runtime.EventType and calls EnqueueEvent.
type agentEventAdapter struct{ rt *runtime.Service }

func (a *agentEventAdapter) EnqueueEvent(ctx context.Context, eventType string, payload []byte) (int64, error) {
	return 0, errNotImplemented
}

var _ agent.EventEnqueuer = (*agentEventAdapter)(nil)

// convoAdapter satisfies brain.ConversationReader over *runtime.Service
// (06 §3.2, 07 §3). runtime.Message and brain.Message are structurally
// identical but distinct named types (same no-cross-import rule); the real
// body calls rt.Recent and maps Role/Text/CreatedAt -> Role/Text/At.
type convoAdapter struct{ rt *runtime.Service }

func (a *convoAdapter) Recent(ctx context.Context, n int) ([]brain.Message, error) {
	return nil, errNotImplemented
}

var _ brain.ConversationReader = (*convoAdapter)(nil)

// slotsAdapter satisfies agent.Slots (05 §4's reconciler read of board
// capacity slot ids). Flagged, not just stubbed: board.Service (03 §4)
// currently exposes only the aggregate WorkerTotal/WorkerFree via GetBoard,
// no per-slot id listing — closing that gap (a board API addition, out of
// this module's scaffold scope) is an open question for the solution phase.
type slotsAdapter struct{ board *board.Service }

func (a *slotsAdapter) WorkerIDs(ctx context.Context) ([]string, error) {
	return nil, errNotImplemented
}

var _ agent.Slots = (*slotsAdapter)(nil)

// logNotifier satisfies runtime.Notifier (02 §10 executor for notify.send).
// v1 descope (07 §6): a structured log line, no real push — the outbox
// contract is unchanged, so 10 swaps this adapter for a real one and
// nothing else. The real body logs the payload (topic/ticket/reason) and
// returns nil unconditionally — a log line cannot fail, and "a rare
// duplicate notification is accepted as benign" (04 §3) applies trivially.
type logNotifier struct{ log *slog.Logger }

func (n *logNotifier) Send(ctx context.Context, payload []byte) error {
	return errNotImplemented
}

var _ runtime.Notifier = (*logNotifier)(nil)

// realClock is the wall-clock Clock both runtime (04 §9) and agent (05 §10)
// want, satisfying both ports with the same value — mechanical, so
// implemented for real rather than stubbed, the same call the skill makes
// for Worker.Nudge.
type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

var (
	_ runtime.Clock = realClock{}
	_ agent.Clock   = realClock{}
)
