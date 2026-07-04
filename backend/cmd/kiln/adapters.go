package main

// Port adapters — the composition root's core job (02 §2, 04 §8 D9): every
// internal/* module depends only on the narrow ports it declares, never on a
// sibling module's concrete type, so this is the only file where board.Service,
// brain.Service, agent.Service, and runtime.Service meet. Two kinds of seam
// show up below:
//
//   - Direct satisfaction: some ports match a service's method set exactly, so
//     the concrete *xxx.Service is injected with no adapter at all —
//     *board.Service satisfies brain.BoardAPI/BoardReader and runtime.Puller;
//     *agent.Service satisfies runtime.AgentRuntime; *api.Hub satisfies
//     runtime.SnapshotPusher/SayPusher; *runtime.Service satisfies
//     api.MessagePoster/MessagesReader and brain.Say.
//   - Adapted satisfaction: the port and the service disagree on a type even
//     though the operation is the same — an outer module's own named Event
//     type vs. runtime's, board's typed (Ticket, error) return vs. runtime's
//     bare error, a plain string vs. a named EventType. Each such pair gets a
//     one-method wrapper below.
//
// Two of these adapters also break the construction cycles (main.go's package
// doc): brainAdapter.inner and agentEventAdapter.rt are filled *after* the
// service they point at is built, so neither service needs to exist before the
// other — late binding through the adapter, no setter on any Service.

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
	"github.com/crabtree-michael/kiln/backend/internal/board"
	"github.com/crabtree-michael/kiln/backend/internal/brain"
	"github.com/crabtree-michael/kiln/backend/internal/runtime"
)

// brainAdapter satisfies runtime.Brain over *brain.Service (04 §2 Brain
// port), mapping runtime.Event → brain.Event field-for-field. inner is set
// after brain.Service is constructed, resolving the runtime↔brain cycle.
type brainAdapter struct{ inner *brain.Service }

func (a *brainAdapter) HandleEvent(ctx context.Context, ev runtime.Event) error {
	if err := a.inner.HandleEvent(ctx, brain.Event{
		ID:        ev.ID,
		Type:      brain.EventType(ev.Type),
		Payload:   ev.Payload,
		CreatedAt: ev.CreatedAt,
	}); err != nil {
		return fmt.Errorf("kiln: brain handle event: %w", err)
	}
	return nil
}

var _ runtime.Brain = (*brainAdapter)(nil)

// blockerAdapter satisfies runtime.Blocker over *board.Service (04 §2
// Blocker port; 03 §7.3's mechanical failure path): convert the id and drop
// the returned Ticket.
type blockerAdapter struct{ inner *board.Service }

func (a *blockerAdapter) MarkBlocked(ctx context.Context, ticketID, reason string) error {
	if _, err := a.inner.MarkBlocked(ctx, board.TicketID(ticketID), reason); err != nil {
		return fmt.Errorf("kiln: mark blocked: %w", err)
	}
	return nil
}

var _ runtime.Blocker = (*blockerAdapter)(nil)

// boardViewAdapter satisfies runtime.BoardReader over *board.Service (08 §3
// feed assembly): map a board.Snapshot to the runtime's local BoardView so the
// runtime never imports internal/board. Blocked tickets become blocker cards
// (BlockedReason dereferenced, "" when nil); shaping tickets awaiting approval
// become proposal cards; the working/blocked counts drive the header summary.
type boardViewAdapter struct{ inner *board.Service }

func (a *boardViewAdapter) BoardView(ctx context.Context) (runtime.BoardView, error) {
	snap, err := a.inner.GetBoard(ctx)
	if err != nil {
		return runtime.BoardView{}, fmt.Errorf("kiln: board view: %w", err)
	}
	view := runtime.BoardView{
		WorkingCount: len(snap.Working),
		BlockedCount: len(snap.Blocked),
		TicketTitles: make(map[string]string),
	}
	// Index every current ticket's title by id so a ticket-tagged update/preview
	// note can render the linked ticket's title as its label (08 §3).
	for _, group := range [][]board.Ticket{
		snap.Shaping, snap.Ready, snap.Blocked, snap.Working, snap.Done,
	} {
		for _, t := range group {
			view.TicketTitles[string(t.ID)] = t.Title
		}
	}
	for _, t := range snap.Blocked {
		reason := ""
		if t.BlockedReason != nil {
			reason = *t.BlockedReason
		}
		view.Blocked = append(view.Blocked, runtime.BoardTicket{
			ID:            string(t.ID),
			Title:         t.Title,
			Body:          t.Body,
			BlockedReason: reason,
			UpdatedAt:     t.UpdatedAt,
		})
	}
	for _, t := range snap.Shaping {
		if !t.ApprovalRequested {
			continue
		}
		view.Proposals = append(view.Proposals, runtime.BoardTicket{
			ID:        string(t.ID),
			Title:     t.Title,
			Body:      t.Body,
			UpdatedAt: t.UpdatedAt,
		})
	}
	return view, nil
}

var _ runtime.BoardReader = (*boardViewAdapter)(nil)

// agentEventAdapter satisfies agent.EventEnqueuer over *runtime.Service
// (05 §2.2's inbound seam): wrap the plain-string event type as
// runtime.EventType. rt is set after runtime.Service is constructed,
// resolving the runtime↔agent cycle.
type agentEventAdapter struct{ rt *runtime.Service }

func (a *agentEventAdapter) EnqueueEvent(ctx context.Context, eventType string, payload []byte) (int64, error) {
	id, err := a.rt.EnqueueEvent(ctx, runtime.EventType(eventType), payload)
	if err != nil {
		return 0, fmt.Errorf("kiln: enqueue event: %w", err)
	}
	return id, nil
}

var _ agent.EventEnqueuer = (*agentEventAdapter)(nil)

// convoAdapter satisfies brain.ConversationReader over *runtime.Service
// (06 §3.2, 07 §3): call rt.Recent and map runtime.Message → brain.Message.
type convoAdapter struct{ rt *runtime.Service }

func (a *convoAdapter) Recent(ctx context.Context, n int) ([]brain.Message, error) {
	msgs, err := a.rt.Recent(ctx, n)
	if err != nil {
		return nil, fmt.Errorf("kiln: read transcript: %w", err)
	}
	out := make([]brain.Message, len(msgs))
	for i, m := range msgs {
		out[i] = brain.Message{Role: brain.MessageRole(m.Role), Text: m.Text, At: m.CreatedAt}
	}
	return out, nil
}

var _ brain.ConversationReader = (*convoAdapter)(nil)

// agentInspectorAdapter bridges *agent.Service to brain.AgentInspector,
// converting the agent module's neutral inspector shapes to the brain's own
// value-copies (brain cannot import internal/agent). No provider handle crosses.
type agentInspectorAdapter struct {
	inner *agent.Service
}

func (a *agentInspectorAdapter) ListAgents(ctx context.Context) ([]brain.AgentInfo, error) {
	infos, err := a.inner.ListAgents(ctx)
	if err != nil {
		return nil, fmt.Errorf("kiln: list agents: %w", err)
	}
	out := make([]brain.AgentInfo, len(infos))
	for i, in := range infos {
		out[i] = brain.AgentInfo{
			WorkerID:  in.WorkerID,
			TicketID:  in.TicketID,
			Status:    brain.AgentStatus(in.Status),
			UpdatedAt: in.UpdatedAt,
		}
	}
	return out, nil
}

func (a *agentInspectorAdapter) GetAgentUpdates(ctx context.Context, workerID string) (brain.AgentUpdate, error) {
	u, err := a.inner.GetAgentUpdates(ctx, workerID)
	if err != nil {
		return brain.AgentUpdate{}, fmt.Errorf("kiln: get agent updates: %w", err)
	}
	return brain.AgentUpdate{
		WorkerID:     u.WorkerID,
		Status:       brain.AgentStatus(u.Status),
		LatestOutput: u.LatestOutput,
		IsError:      u.IsError,
		At:           u.At,
	}, nil
}

var _ brain.AgentInspector = (*agentInspectorAdapter)(nil)

// workerLister is the concrete board capacity-slot read the composition root
// backs agent.Slots with (05 §4). Satisfied by *board/postgres.Store's
// WorkerIDs — a concrete adapter helper like ReconcileWorkers, not part of
// board.Store, so the workers table stays board-owned (03 I8).
type workerLister interface {
	WorkerIDs(ctx context.Context) ([]string, error)
}

// slotsAdapter satisfies agent.Slots over the board store's WorkerIDs read.
type slotsAdapter struct{ store workerLister }

func (a *slotsAdapter) WorkerIDs(ctx context.Context) ([]string, error) {
	ids, err := a.store.WorkerIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("kiln: list worker ids: %w", err)
	}
	return ids, nil
}

var _ agent.Slots = (*slotsAdapter)(nil)

// logNotifier satisfies runtime.Notifier (02 §10 executor for notify.send).
// v1 descope (07 §6): a structured log line, no real push — the outbox
// contract is unchanged, so 10 swaps this adapter for a real one and nothing
// else. A log line cannot fail, and "a rare duplicate notification is accepted
// as benign" (04 §3) applies trivially.
type logNotifier struct{ log *slog.Logger }

func (n *logNotifier) Send(_ context.Context, payload []byte) error {
	n.log.Info("notify.send", "payload", string(payload))
	return nil
}

var _ runtime.Notifier = (*logNotifier)(nil)

// realClock is the wall-clock Clock both runtime (04 §9) and agent (05 §10)
// want, satisfying both ports with the same value.
type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

var (
	_ runtime.Clock = realClock{}
	_ agent.Clock   = realClock{}
)
