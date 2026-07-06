package steward

import (
	"context"
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

// Board is the sweep's port onto the board: read the Working set, poke a
// stalled agent (a plain Working→Working new turn), and escalate to Blocked.
type Board interface {
	WorkingTickets(ctx context.Context) ([]WorkingTicket, error)
	// Poke delivers the generic continue signal to the ticket's agent.
	Poke(ctx context.Context, ticketID string) error
	// Block moves the ticket Working→Blocked with reason.
	Block(ctx context.Context, ticketID, reason string) error
}

// Agents is the sweep's port onto the agent runtime: each live worker's
// neutral status keyed by worker slot id. No provider handle crosses.
type Agents interface {
	States(ctx context.Context) (map[string]AgentState, error)
}

// Feed is the sweep's port onto the feed: post the feed-only poke card for a
// ticket (its title with a 👉, no body — not a toast, not a blocker).
type Feed interface {
	PostPoke(ctx context.Context, ticketID string) error
}

// Service is the mechanical stall watchdog. Constructed at the composition root
// over the ports above.
type Service struct {
	board  Board
	agents Agents
	feed   Feed
	store  Store
	clock  Clock
	cfg    Config
}

// NewService assembles the watchdog. cfg's zero fields fall back to defaults.
func NewService(board Board, agents Agents, feed Feed, store Store, clock Clock, cfg Config) *Service {
	return &Service{
		board:  board,
		agents: agents,
		feed:   feed,
		store:  store,
		clock:  clock,
		cfg:    cfg.withDefaults(),
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

// sweep is one deterministic pass: join the Working set with agent status and
// the poke records, decide per ticket, then prune records for tickets that have
// left Working. A read failure aborts the pass (retried next tick) rather than
// acting on a partial view.
func (s *Service) sweep(ctx context.Context) {
	working, err := s.board.WorkingTickets(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "steward: read working tickets", "err", err)
		return
	}
	states, err := s.agents.States(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "steward: read agent states", "err", err)
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
	live := make(map[string]struct{}, len(working))
	for _, t := range working {
		live[t.ID] = struct{}{}
		state, ok := states[t.WorkerID]
		if !ok {
			// No agent info for this worker yet — can't assess; leave it.
			continue
		}
		rec, wasPoked := poked[t.ID]
		s.evaluate(ctx, t, state, rec, wasPoked, now)
	}

	// Prune records whose ticket is no longer Working — the episode is over
	// (done/blocked/re-shaped), so a future stall starts from a clean slate.
	for id := range poked {
		if _, ok := live[id]; ok {
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
	ctx context.Context, t WorkingTicket, state AgentState,
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
			s.block(ctx, t, reasonErroredAfterPoke)
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
			s.block(ctx, t, reasonStalledTwice)
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
		s.poke(ctx, t, now)
	}
}

// poke sends the generic continue signal, posts the feed poke card, and records
// the poke. The board transition gates the rest: if the poke can't be delivered
// there is nothing to announce or remember. A feed-post failure is logged but
// does not abort — the poke was delivered and must still be recorded so a
// re-stall escalates rather than re-pokes.
func (s *Service) poke(ctx context.Context, t WorkingTicket, now time.Time) {
	if err := s.board.Poke(ctx, t.ID); err != nil {
		slog.ErrorContext(ctx, "steward: poke agent", "ticket_id", t.ID, "err", err)
		return
	}
	if err := s.feed.PostPoke(ctx, t.ID); err != nil {
		slog.ErrorContext(ctx, "steward: post poke card", "ticket_id", t.ID, "err", err)
	}
	if err := s.store.Upsert(ctx, t.ID, t.WorkerID, now); err != nil {
		slog.ErrorContext(ctx, "steward: record poke", "ticket_id", t.ID, "err", err)
	}
	slog.InfoContext(ctx, "steward: poked stalled agent", "ticket_id", t.ID, "worker_id", t.WorkerID)
}

// block escalates the ticket to Blocked and clears its poke record.
func (s *Service) block(ctx context.Context, t WorkingTicket, reason string) {
	if err := s.board.Block(ctx, t.ID, reason); err != nil {
		slog.ErrorContext(ctx, "steward: block stalled ticket", "ticket_id", t.ID, "err", err)
		return
	}
	if err := s.store.Delete(ctx, t.ID); err != nil {
		slog.ErrorContext(ctx, "steward: clear poke record after block", "ticket_id", t.ID, "err", err)
	}
	slog.InfoContext(ctx, "steward: blocked stalled ticket", "ticket_id", t.ID, "worker_id", t.WorkerID)
}
