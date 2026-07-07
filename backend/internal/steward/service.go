package steward

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Neutral agent status strings the sweep reasons over — mirrors the agent
// module's AgentStatus values by value (the composition-root adapter maps
// agent.AgentStatus → these), so this module never imports internal/agent.
const (
	statusBuilding = "building" // a turn in flight — leave alone
	statusStarting = "starting" // session provisioning — leave alone
	statusIdle     = "idle"     // alive, no turn — poke-eligible
	statusStopped  = "stopped"  // session auto-stopped — poke-eligible
	statusErrored  = "errored"  // terminal failure — block-eligible after a poke
)

// Reasons the sweep sets when it escalates a ticket to Blocked. Phrased for the
// user, who decides what to do next.
const (
	reasonStalledTwice = "The agent went idle again after a poke and made no progress. " +
		"It looks stuck — take a look and re-send it when you're ready."
	reasonErroredAfterPoke = "The agent reported an error after a poke. " +
		"It looks stuck — take a look and re-send it when you're ready."
)

// Clock is the sweep's testable time source (matches runtime.Clock /
// agent.Clock structurally, so the composition root's realClock satisfies it).
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// WorkingTicket is one Working ticket with its bound worker slot — the sweep's
// unit of work. The board is the source of truth for which tickets are Working
// (03 I8); the composition-root adapter fills these from board.GetBoard.
type WorkingTicket struct {
	ID       string
	WorkerID string
}

// AgentState is the neutral view of one worker's agent the sweep needs:
// its running status and the time of its last activity (last turn advance).
type AgentState struct {
	Status    string
	UpdatedAt time.Time
}

// Projects resolves the set of project ids the sweep iterates. Declared
// locally (modules never import each other); the composition root adapts the
// identity module's project registry onto it.
type Projects interface {
	ProjectIDs(ctx context.Context) ([]string, error)
}

// Board is the sweep's port onto one project's board: read the Working set,
// poke a stalled agent (a plain Working→Working new turn), and escalate to
// Blocked. Every call is scoped by projectID.
type Board interface {
	WorkingTickets(ctx context.Context, projectID string) ([]WorkingTicket, error)
	// Poke delivers the generic continue signal to the ticket's agent.
	Poke(ctx context.Context, projectID, ticketID string) error
	// Block moves the ticket Working→Blocked with reason.
	Block(ctx context.Context, projectID, ticketID, reason string) error
}

// Agents is the sweep's port onto the agent runtime: one project's live
// workers' neutral status keyed by worker slot id. No provider handle crosses.
type Agents interface {
	States(ctx context.Context, projectID string) (map[string]AgentState, error)
}

// Feed is the sweep's port onto the feed: post the feed-only poke card for a
// ticket (its title with a 👉, no body — not a toast, not a blocker) into its
// project's feed.
type Feed interface {
	PostPoke(ctx context.Context, projectID, ticketID string) error
}

// Service is the mechanical stall watchdog. Constructed at the composition root
// over the ports above.
type Service struct {
	projects Projects
	board    Board
	agents   Agents
	feed     Feed
	store    Store
	clock    Clock
	cfg      Config
}

// NewService assembles the watchdog. cfg's zero fields fall back to defaults.
func NewService(
	projects Projects, board Board, agents Agents, feed Feed,
	store Store, clock Clock, cfg Config,
) *Service {
	return &Service{
		projects: projects,
		board:    board,
		agents:   agents,
		feed:     feed,
		store:    store,
		clock:    clock,
		cfg:      cfg.withDefaults(),
	}
}

// Run drives the sweep on the clock until ctx is cancelled. Mirrors the agent
// module's loop: a plain interval tick, each firing one full sweep. Never
// returns an error — a failed sweep is logged and retried next tick.
func (s *Service) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-s.clock.After(s.cfg.Interval):
			s.sweep(ctx)
		}
	}
}

// sweep is one deterministic pass over every project: resolve the project set,
// load the (global) poke records once, sweep each project's Working set against
// its own board/agent scope, then prune records for tickets that have left
// Working. A project that fails mid-sweep is logged and skipped — the others
// still run — and its records are exempt from pruning (no view, no verdict).
// A failure resolving projects or listing records aborts the whole pass
// (retried next tick) rather than acting on a partial view.
func (s *Service) sweep(ctx context.Context) {
	projectIDs, err := s.projects.ProjectIDs(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "steward: resolve project ids", "err", err)
		return
	}
	records, err := s.store.List(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "steward: list poke records", "err", err)
		return
	}
	poked := make(map[string]PokeRecord, len(records))
	for _, r := range records {
		poked[r.TicketID] = r
	}

	now := s.clock.Now()
	live := make(map[string]struct{})
	swept := make(map[string]bool, len(projectIDs))
	allSwept := true
	for _, pid := range projectIDs {
		if err := s.sweepProject(ctx, pid, poked, live, now); err != nil {
			slog.ErrorContext(ctx, "steward: sweep project", "project_id", pid, "err", err)
			allSwept = false
			continue
		}
		swept[pid] = true
	}

	s.prune(ctx, poked, live, swept, allSwept)
}

// sweepProject joins one project's Working set with its agent states and the
// poke records, deciding per ticket, and folds the project's Working tickets
// into live (the cross-project prune set). A read failure returns the error so
// the caller can skip just this project.
func (s *Service) sweepProject(
	ctx context.Context, projectID string,
	poked map[string]PokeRecord, live map[string]struct{}, now time.Time,
) error {
	working, err := s.board.WorkingTickets(ctx, projectID)
	if err != nil {
		return fmt.Errorf("read working tickets: %w", err)
	}
	states, err := s.agents.States(ctx, projectID)
	if err != nil {
		return fmt.Errorf("read agent states: %w", err)
	}
	for _, t := range working {
		live[t.ID] = struct{}{}
		state, ok := states[t.WorkerID]
		if !ok {
			// No agent info for this worker yet — can't assess; leave it.
			continue
		}
		rec, wasPoked := poked[t.ID]
		s.evaluate(ctx, projectID, t, state, rec, wasPoked, now)
	}
	return nil
}

// prune removes records whose ticket is no longer Working — the episode is
// over (done/blocked/re-shaped), so a future stall starts from a clean slate.
// Ticket ids are globally unique, so a record is correlated to its project via
// the stored project_id: a record is only pruned once its project's Working
// set was actually observed this pass (swept), or — for records without a
// resolvable project (legacy NULL rows, or a project that no longer resolves)
// — only on a pass where every project swept cleanly.
func (s *Service) prune(
	ctx context.Context, poked map[string]PokeRecord,
	live map[string]struct{}, swept map[string]bool, allSwept bool,
) {
	for id, rec := range poked {
		if _, ok := live[id]; ok {
			continue
		}
		if !swept[rec.ProjectID] && !allSwept {
			continue
		}
		if err := s.store.Delete(ctx, id); err != nil {
			slog.ErrorContext(ctx, "steward: prune poke record", "ticket_id", id, "err", err)
		}
	}
}

// evaluate applies the deterministic rule to one Working ticket. Exactly one
// poke per Working episode; a subsequent sustained stall or a post-poke error
// escalates to Blocked. An agent that is building/starting is always left alone.
func (s *Service) evaluate(
	ctx context.Context, projectID string, t WorkingTicket, state AgentState,
	rec PokeRecord, wasPoked bool, now time.Time,
) {
	switch state.Status {
	case statusBuilding, statusStarting:
		// In flight — no safe way to tell slow-but-fine from a hang. Leave it,
		// and keep any existing poke record so a later stall in the same episode
		// still escalates rather than re-pokes.
		return
	case statusErrored:
		// A fresh error (no poke yet) is the brain's to interpret via the
		// turn_completed event; the watchdog only acts on an error that follows
		// its own poke.
		if wasPoked {
			s.block(ctx, projectID, t, reasonErroredAfterPoke)
		}
		return
	case statusIdle, statusStopped:
		// Poke-eligible; fall through.
	default:
		return
	}

	if wasPoked {
		// Already poked this episode. If it has been idle/stopped for a full
		// threshold since the poke, the poke did not take — escalate.
		if now.Sub(rec.PokedAt) >= s.cfg.Stall {
			s.block(ctx, projectID, t, reasonStalledTwice)
		}
		return
	}

	// Never poked. A zero UpdatedAt means the agent has no activity baseline yet
	// (e.g. a just-pulled ticket whose first turn is still being recorded) —
	// skip rather than treat the epoch as an ancient stall.
	if state.UpdatedAt.IsZero() {
		return
	}
	if now.Sub(state.UpdatedAt) >= s.cfg.Stall {
		s.poke(ctx, projectID, t, now)
	}
}

// poke sends the generic continue signal, posts the feed poke card, and records
// the poke. The board transition gates the rest: if the poke can't be delivered
// there is nothing to announce or remember. A feed-post failure is logged but
// does not abort — the poke was delivered and must still be recorded so a
// re-stall escalates rather than re-pokes.
func (s *Service) poke(ctx context.Context, projectID string, t WorkingTicket, now time.Time) {
	if err := s.board.Poke(ctx, projectID, t.ID); err != nil {
		slog.ErrorContext(ctx, "steward: poke agent", "project_id", projectID, "ticket_id", t.ID, "err", err)
		return
	}
	if err := s.feed.PostPoke(ctx, projectID, t.ID); err != nil {
		slog.ErrorContext(ctx, "steward: post poke card", "project_id", projectID, "ticket_id", t.ID, "err", err)
	}
	if err := s.store.Upsert(ctx, projectID, t.ID, t.WorkerID, now); err != nil {
		slog.ErrorContext(ctx, "steward: record poke", "project_id", projectID, "ticket_id", t.ID, "err", err)
	}
	slog.InfoContext(ctx, "steward: poked stalled agent",
		"project_id", projectID, "ticket_id", t.ID, "worker_id", t.WorkerID)
}

// block escalates the ticket to Blocked and clears its poke record.
func (s *Service) block(ctx context.Context, projectID string, t WorkingTicket, reason string) {
	if err := s.board.Block(ctx, projectID, t.ID, reason); err != nil {
		slog.ErrorContext(ctx, "steward: block stalled ticket", "project_id", projectID, "ticket_id", t.ID, "err", err)
		return
	}
	if err := s.store.Delete(ctx, t.ID); err != nil {
		slog.ErrorContext(ctx, "steward: clear poke record after block", "ticket_id", t.ID, "err", err)
	}
	slog.InfoContext(ctx, "steward: blocked stalled ticket",
		"project_id", projectID, "ticket_id", t.ID, "worker_id", t.WorkerID)
}
