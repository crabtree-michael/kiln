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
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
	"github.com/crabtree-michael/kiln/backend/internal/api"
	"github.com/crabtree-michael/kiln/backend/internal/board"
	"github.com/crabtree-michael/kiln/backend/internal/brain"
	"github.com/crabtree-michael/kiln/backend/internal/repo"
	"github.com/crabtree-michael/kiln/backend/internal/runtime"
	"github.com/crabtree-michael/kiln/backend/internal/steward"
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
// (BlockedReason dereferenced, "" when nil); every shaping ticket becomes a
// proposal card — a ticket in Shaping is implicitly a proposal awaiting review
// (08 §5, superseding D5's approval_requested gate); the working/blocked counts
// drive the header summary.
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

// feedReaderAdapter satisfies brain.FeedReader over *runtime.Service (06 §4
// amended, 08 §7): call rt.ListNotifications and map runtime.Notification →
// brain.Update, so the brain can list the active feed cards it may edit or
// retract without importing internal/runtime.
type feedReaderAdapter struct{ rt *runtime.Service }

func (a *feedReaderAdapter) ListUpdates(ctx context.Context) ([]brain.Update, error) {
	notes, err := a.rt.ListNotifications(ctx)
	if err != nil {
		return nil, fmt.Errorf("kiln: list updates: %w", err)
	}
	out := make([]brain.Update, len(notes))
	for i, n := range notes {
		u := brain.Update{ID: n.ID, Kind: string(n.Kind), Body: n.Body, CreatedAt: n.CreatedAt}
		if n.TicketID != nil {
			u.TicketID = *n.TicketID
		}
		if n.ImageURL != nil {
			u.ImageURL = *n.ImageURL
		}
		out[i] = u
	}
	return out, nil
}

var _ brain.FeedReader = (*feedReaderAdapter)(nil)

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

// agentStatusAdapter bridges *agent.Service to api.AgentInspector, converting
// the agent module's neutral AgentInfo to the api's own value-copy (the api
// never imports internal/agent). inner is set after agent.Service is built,
// resolving the api-hub↔agent cycle (the hub is the agent's board refresher).
// No provider handle crosses. Amended 2026-07-05 for the Streams view's real
// session status.
type agentStatusAdapter struct{ inner *agent.Service }

func (a *agentStatusAdapter) ListAgents(ctx context.Context) ([]api.AgentInfo, error) {
	infos, err := a.inner.ListAgents(ctx)
	if err != nil {
		return nil, fmt.Errorf("kiln: list agents for board: %w", err)
	}
	out := make([]api.AgentInfo, len(infos))
	for i, in := range infos {
		out[i] = api.AgentInfo{
			WorkerID: in.WorkerID,
			TicketID: in.TicketID,
			Status:   string(in.Status),
		}
	}
	return out, nil
}

var _ api.AgentInspector = (*agentStatusAdapter)(nil)

// boardRefreshAdapter satisfies agent.BoardRefresher over *api.Hub: the agent's
// liveness loop nudges the hub to re-push the board when a silent auto-stop
// changes a worker's status, so Streams updates without a manual nudge (amended
// 2026-07-05). A thin wrapper because the port's RefreshBoard and the hub's
// PushBoard are the same operation under different names.
type boardRefreshAdapter struct{ hub *api.Hub }

func (a *boardRefreshAdapter) RefreshBoard(ctx context.Context) error {
	if err := a.hub.PushBoard(ctx); err != nil {
		return fmt.Errorf("kiln: refresh board: %w", err)
	}
	return nil
}

var _ agent.BoardRefresher = (*boardRefreshAdapter)(nil)

// stewardBoardAdapter satisfies steward.Board over *board.Service: the Working
// set (each ticket with its bound worker slot), a poke (a Working→Working new
// turn carrying the generic "poke" message), and the escalation to Blocked. The
// steward never imports internal/board — this is the only seam.
type stewardBoardAdapter struct{ inner *board.Service }

func (a *stewardBoardAdapter) WorkingTickets(ctx context.Context) ([]steward.WorkingTicket, error) {
	snap, err := a.inner.GetBoard(ctx)
	if err != nil {
		return nil, fmt.Errorf("kiln: steward working tickets: %w", err)
	}
	out := make([]steward.WorkingTicket, 0, len(snap.Working))
	for _, t := range snap.Working {
		if t.WorkerID == nil {
			continue // a Working ticket always binds a worker (03 I3); skip if not.
		}
		out = append(out, steward.WorkingTicket{ID: string(t.ID), WorkerID: string(*t.WorkerID)})
	}
	return out, nil
}

func (a *stewardBoardAdapter) Poke(ctx context.Context, ticketID string) error {
	if _, err := a.inner.SendToAgent(ctx, board.TicketID(ticketID), "poke"); err != nil {
		return fmt.Errorf("kiln: steward poke: %w", err)
	}
	return nil
}

func (a *stewardBoardAdapter) Block(ctx context.Context, ticketID, reason string) error {
	if _, err := a.inner.MarkBlocked(ctx, board.TicketID(ticketID), reason); err != nil {
		return fmt.Errorf("kiln: steward block: %w", err)
	}
	return nil
}

var _ steward.Board = (*stewardBoardAdapter)(nil)

// stewardAgentAdapter satisfies steward.Agents over *agent.Service.ListAgents:
// each live worker's neutral status + last-activity time, keyed by worker slot
// id. The status strings match steward's by value; no provider handle crosses.
type stewardAgentAdapter struct{ inner *agent.Service }

func (a *stewardAgentAdapter) States(ctx context.Context) (map[string]steward.AgentState, error) {
	infos, err := a.inner.ListAgents(ctx)
	if err != nil {
		return nil, fmt.Errorf("kiln: steward agent states: %w", err)
	}
	out := make(map[string]steward.AgentState, len(infos))
	for _, in := range infos {
		out[in.WorkerID] = steward.AgentState{Status: string(in.Status), UpdatedAt: in.UpdatedAt}
	}
	return out, nil
}

var _ steward.Agents = (*stewardAgentAdapter)(nil)

// stewardFeedAdapter satisfies steward.Feed over *runtime.Service.PostPoke: post
// the feed-only poke card (ticket title + 👉, no body).
type stewardFeedAdapter struct{ inner *runtime.Service }

func (a *stewardFeedAdapter) PostPoke(ctx context.Context, ticketID string) error {
	if err := a.inner.PostPoke(ctx, ticketID); err != nil {
		return fmt.Errorf("kiln: steward post poke: %w", err)
	}
	return nil
}

var _ steward.Feed = (*stewardFeedAdapter)(nil)

// repoShellAdapter bridges *repo.Shell to brain.RepoShell, converting the
// repo module's Result to the brain's own value-copy (brain cannot import
// internal/repo). *repo.Shell.Run never errors — a disabled shell or a failed
// command is expressed in the Result — so this adapter always returns nil.
type repoShellAdapter struct {
	inner *repo.Shell
}

func (a *repoShellAdapter) Run(ctx context.Context, command string) (brain.RepoResult, error) {
	r := a.inner.Run(ctx, command)
	return brain.RepoResult{
		Output:      r.Output,
		ExitCode:    r.ExitCode,
		TimedOut:    r.TimedOut,
		Truncated:   r.Truncated,
		Unavailable: r.Unavailable,
		Reason:      r.Reason,
	}, nil
}

var _ brain.RepoShell = (*repoShellAdapter)(nil)

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
	_ steward.Clock = realClock{}
)

// resetCoordinator satisfies api.Resetter (docs/superpowers/specs/
// 2026-07-04-debug-reset-session-design.md): the developer "fresh session"
// reset that the /debug client fires. It is composition-root-only because it
// spans two modules — a raw DB truncate and the agent service's worker
// teardown — neither of which owns the other's state. tables and workers are
// narrow ports so the ordering is unit-testable against fakes.
type resetCoordinator struct {
	tables   stateTruncator
	workers  workerResetter
	pool     poolReconciler
	poolSize int
}

// newResetCoordinator wires the reset over the shared pool, the agent service,
// and the board's worker-pool store — kept here so buildGraph reads as one line.
func newResetCoordinator(db *sql.DB, workers workerResetter, pool poolReconciler, poolSize int) *resetCoordinator {
	return &resetCoordinator{
		tables:   &dbTruncator{db: db},
		workers:  workers,
		pool:     pool,
		poolSize: poolSize,
	}
}

// stateTruncator wipes every runtime state table (board, transcript, queue,
// notifications) in one shot; schema_migrations is left intact.
type stateTruncator interface {
	TruncateState(ctx context.Context) error
}

// workerResetter tears down live agent sandboxes and clears the module's
// in-memory worker cache. Satisfied directly by *agent.Service.
type workerResetter interface {
	Reset(ctx context.Context) error
}

// poolReconciler re-seeds the board's worker-slot pool to n rows — the same
// startup call (03 §8). The truncate empties the workers table, so without this
// a fresh session would have zero capacity until the next backend restart.
// Satisfied directly by *boardpg.Store.
type poolReconciler interface {
	ReconcileWorkers(ctx context.Context, n int) error
}

// Reset returns the system to a fresh session in three steps: (1) truncate the
// state tables, which also empties the board's worker slots so nothing is
// "wanted"; (2) tear down the live sandboxes and clear the agent's in-memory
// cache while the wanted set is empty (a bare truncate leaves stale cached
// handles — the reason a manual reset previously needed a backend restart);
// (3) re-seed the worker pool to its configured size so the agent reconciler
// provisions a fresh idle pool, exactly as at startup.
func (c *resetCoordinator) Reset(ctx context.Context) error {
	if err := c.tables.TruncateState(ctx); err != nil {
		return fmt.Errorf("kiln: reset truncate state: %w", err)
	}
	if err := c.workers.Reset(ctx); err != nil {
		return fmt.Errorf("kiln: reset workers: %w", err)
	}
	if err := c.pool.ReconcileWorkers(ctx, c.poolSize); err != nil {
		return fmt.Errorf("kiln: reset reconcile worker pool: %w", err)
	}
	return nil
}

var (
	_ api.Resetter   = (*resetCoordinator)(nil)
	_ workerResetter = (*agent.Service)(nil)
	_ stateTruncator = (*dbTruncator)(nil)
)

// dbTruncator is the stateTruncator over the shared Postgres pool: one
// TRUNCATE across every state table. RESTART IDENTITY resets sequences so a
// fresh session starts ids from 1; CASCADE covers any FK edges between them.
type dbTruncator struct{ db *sql.DB }

const truncateStateSQL = `TRUNCATE tickets, workers, outbox, messages, events, ` +
	`agent_turns, notifications, steward_pokes RESTART IDENTITY CASCADE`

func (t *dbTruncator) TruncateState(ctx context.Context) error {
	if _, err := t.db.ExecContext(ctx, truncateStateSQL); err != nil {
		return fmt.Errorf("kiln: truncate state tables: %w", err)
	}
	return nil
}
