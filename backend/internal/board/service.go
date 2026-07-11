package board

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Service is the Board API (03 §4) — the only mutation surface for board
// state. Callers: the brain (02 §6) for every operation except RunPull, which
// is never a brain tool (03 I6); the runtime for RunPull (driven by
// pull.evaluate entries) and for the mechanical failure path of MarkBlocked
// (03 §7.3, 04 §3).
//
// Every operation is scoped to one project (11 §3): projectID is the tenant
// key, the first parameter after ctx on every method, passed through to the
// store so no read or write ever crosses a project boundary.
//
// Every mutation is one transaction — lock the ticket, verify the
// precondition, apply the change, append outbox rows, commit (03 §6) — and,
// beyond the emissions noted per operation, every mutation emits
// board.updated. Precondition failures are typed errors (ErrNotFound,
// ErrInvalidTransition), never partial writes or silent no-ops (03 D8).
type Service struct {
	store Store
}

// NewService wires the Board API over its persistence port.
func NewService(store Store) *Service { return &Service{store: store} }

// ShapePatch is ShapeTicket's input; nil fields are left unchanged.
type ShapePatch struct {
	Title    *string
	Body     *string
	Priority *int // higher pulls first; there is no separate reprioritize operation (03 §4)
}

// CreateTicket creates a ticket in shaping (03 §4) under the project.
// Precondition: title non-empty (ErrEmptyTitle otherwise, before any write).
func (s *Service) CreateTicket(ctx context.Context, projectID, title, body string) (Ticket, error) {
	if title == "" {
		return Ticket{}, ErrEmptyTitle
	}
	var out Ticket
	err := s.store.Tx(ctx, func(tx Tx) error {
		created, err := tx.InsertTicket(ctx, projectID, Ticket{
			Title: title,
			Body:  body,
			State: StateShaping,
		})
		if err != nil {
			return fmt.Errorf("board: insert ticket: %w", err)
		}
		if err := tx.AppendOutbox(ctx, projectID, Emission{Topic: TopicBoardUpdated}); err != nil {
			return fmt.Errorf("board: append board.updated: %w", err)
		}
		// A ticket is created in shaping, and every shaping ticket is a
		// proposal card (08 §5, superseding D5's approval_requested gate), so
		// the feed must reassemble to show it.
		if err := tx.AppendOutbox(ctx, projectID, Emission{Topic: TopicFeedUpdated, Payload: FeedUpdatedPayload{
			Title: created.Title,
			Verb:  FeedVerbProposal,
		}}); err != nil {
			return fmt.Errorf("board: append feed.updated: %w", err)
		}
		out = created
		return nil
	})
	if err != nil {
		return Ticket{}, fmt.Errorf("board: create ticket: %w", err)
	}
	logTransition(ctx, "create_ticket", projectID, string(out.ID), "", out.State)
	return out, nil
}

// ShapeTicket updates a ticket's fields while it is still in Backlog; the
// state is unchanged (03 §4).
// Precondition: state ∈ {shaping, ready}.
func (s *Service) ShapeTicket(ctx context.Context, projectID string, id TicketID, patch ShapePatch) (Ticket, error) {
	return s.mutate(ctx, projectID, "shape_ticket", id, func(ctx context.Context, tx Tx, t *Ticket) (Ticket, error) {
		if t.State != StateShaping && t.State != StateReady {
			return Ticket{}, &ErrInvalidTransition{From: t.State, Attempted: "ShapeTicket"}
		}
		if patch.Title != nil {
			t.Title = *patch.Title
		}
		if patch.Body != nil {
			t.Body = *patch.Body
		}
		if patch.Priority != nil {
			t.Priority = *patch.Priority
		}
		updated, err := tx.UpdateTicket(ctx, projectID, *t)
		if err != nil {
			return Ticket{}, fmt.Errorf("board: update ticket: %w", err)
		}
		// A shaping ticket is a proposal card (08 §5); reshaping it changes the
		// card's title/summary, so the feed must reassemble. A ready ticket has
		// no feed surface, so it emits board.updated only.
		if t.State == StateShaping {
			if err := tx.AppendOutbox(ctx, projectID, Emission{Topic: TopicFeedUpdated, Payload: FeedUpdatedPayload{
				Title: updated.Title,
				Verb:  FeedVerbReshaped,
			}}); err != nil {
				return Ticket{}, fmt.Errorf("board: append feed.updated: %w", err)
			}
		}
		return updated, nil
	})
}

// RequestApproval surfaces a shaping ticket to the user as a proposal awaiting
// approval (08 §5). Sets ApprovalRequested = true; the ticket stays in shaping.
// Precondition: state = shaping. Emits feed.updated (a proposal card appears).
func (s *Service) RequestApproval(ctx context.Context, projectID string, id TicketID) (Ticket, error) {
	return s.mutate(ctx, projectID, "request_approval", id, func(ctx context.Context, tx Tx, t *Ticket) (Ticket, error) {
		if t.State != StateShaping {
			return Ticket{}, &ErrInvalidTransition{From: t.State, Attempted: "RequestApproval"}
		}
		t.ApprovalRequested = true
		updated, err := tx.UpdateTicket(ctx, projectID, *t)
		if err != nil {
			return Ticket{}, fmt.Errorf("board: update ticket: %w", err)
		}
		if err := tx.AppendOutbox(ctx, projectID, Emission{Topic: TopicFeedUpdated, Payload: FeedUpdatedPayload{
			Title: updated.Title,
			Verb:  FeedVerbProposal,
		}}); err != nil {
			return Ticket{}, fmt.Errorf("board: append feed.updated: %w", err)
		}
		return updated, nil
	})
}

// MarkReady moves shaping → ready and sets ready_at (03 §4). Clears any pending
// approval request — the proposal is resolved once the ticket is queued (08 §5).
// Precondition: state = shaping. Emits pull.evaluate, feed.updated, and a
// "queued" activity toast (08 §B).
func (s *Service) MarkReady(ctx context.Context, projectID string, id TicketID) (Ticket, error) {
	return s.mutate(ctx, projectID, "mark_ready", id, func(ctx context.Context, tx Tx, t *Ticket) (Ticket, error) {
		if t.State != StateShaping {
			return Ticket{}, &ErrInvalidTransition{From: t.State, Attempted: "MarkReady"}
		}
		now := time.Now().UTC()
		t.State = StateReady
		t.ReadyAt = &now
		t.ApprovalRequested = false
		updated, err := tx.UpdateTicket(ctx, projectID, *t)
		if err != nil {
			return Ticket{}, fmt.Errorf("board: update ticket: %w", err)
		}
		if err := tx.AppendOutbox(ctx, projectID, Emission{Topic: TopicPullEvaluate}); err != nil {
			return Ticket{}, fmt.Errorf("board: append pull.evaluate: %w", err)
		}
		if err := tx.AppendOutbox(ctx, projectID, Emission{Topic: TopicFeedUpdated, Payload: FeedUpdatedPayload{
			Title: updated.Title,
			Verb:  FeedVerbQueued,
		}}); err != nil {
			return Ticket{}, fmt.Errorf("board: append feed.updated: %w", err)
		}
		if err := tx.AppendOutbox(ctx, projectID, Emission{Topic: TopicActivityToast, Payload: ToastPayload{
			Verb:        "queued",
			TicketID:    updated.ID,
			TicketTitle: updated.Title,
		}}); err != nil {
			return Ticket{}, fmt.Errorf("board: append activity.toast: %w", err)
		}
		return updated, nil
	})
}

// SendToAgent covers both 01 §5 rows — Blocked→Working (resume with the
// user's answer) and Working→Working (a new turn). Result state is working;
// blocked_reason is cleared (03 §4).
// Precondition: state ∈ {working, blocked}. Emits agent.send.
func (s *Service) SendToAgent(ctx context.Context, projectID string, id TicketID, instruction string) (Ticket, error) {
	return s.mutate(ctx, projectID, "send_to_agent", id, func(ctx context.Context, tx Tx, t *Ticket) (Ticket, error) {
		if !t.State.Active() || t.WorkerID == nil {
			return Ticket{}, &ErrInvalidTransition{From: t.State, Attempted: "SendToAgent"}
		}
		// A resume out of Blocked is a user-visible nudge (08 §5); a working →
		// working new turn is not (the blocker card was the only feed surface).
		leavingBlocked := t.State == StateBlocked
		worker := *t.WorkerID
		t.State = StateWorking
		t.BlockedReason = nil
		updated, err := tx.UpdateTicket(ctx, projectID, *t)
		if err != nil {
			return Ticket{}, fmt.Errorf("board: update ticket: %w", err)
		}
		if err := tx.AppendOutbox(ctx, projectID, Emission{Topic: TopicAgentSend, Payload: SendPayload{
			TicketID: updated.ID,
			WorkerID: worker,
			Message:  instruction,
		}}); err != nil {
			return Ticket{}, fmt.Errorf("board: append agent.send: %w", err)
		}
		if leavingBlocked {
			if err := tx.AppendOutbox(ctx, projectID, Emission{Topic: TopicFeedUpdated, Payload: FeedUpdatedPayload{
				Title: updated.Title,
				Verb:  FeedVerbNudged,
			}}); err != nil {
				return Ticket{}, fmt.Errorf("board: append feed.updated: %w", err)
			}
			if err := tx.AppendOutbox(ctx, projectID, Emission{Topic: TopicActivityToast, Payload: ToastPayload{
				Verb:        "nudged",
				TicketID:    updated.ID,
				TicketTitle: updated.Title,
			}}); err != nil {
				return Ticket{}, fmt.Errorf("board: append activity.toast: %w", err)
			}
		}
		return updated, nil
	})
}

// MarkBlocked moves working → blocked with the reason the user must decide on
// — or the failure being surfaced when the runtime calls it mechanically
// (crash/timeout, exhausted delivery retries — 03 §7.3, 04 §3).
// Precondition: state = working. Emits notify.send.
func (s *Service) MarkBlocked(ctx context.Context, projectID string, id TicketID, reason string) (Ticket, error) {
	return s.mutate(ctx, projectID, "mark_blocked", id, func(ctx context.Context, tx Tx, t *Ticket) (Ticket, error) {
		if t.State != StateWorking {
			return Ticket{}, &ErrInvalidTransition{From: t.State, Attempted: "MarkBlocked"}
		}
		r := reason
		t.State = StateBlocked
		t.BlockedReason = &r
		updated, err := tx.UpdateTicket(ctx, projectID, *t)
		if err != nil {
			return Ticket{}, fmt.Errorf("board: update ticket: %w", err)
		}
		if err := tx.AppendOutbox(ctx, projectID, Emission{Topic: TopicNotifySend, Payload: NotifyPayload{
			TicketID: updated.ID,
			Title:    updated.Title,
			Reason:   reason,
		}}); err != nil {
			return Ticket{}, fmt.Errorf("board: append notify.send: %w", err)
		}
		// A blocker becomes a feed card (08 §5); no toast — the card is the surface.
		if err := tx.AppendOutbox(ctx, projectID, Emission{Topic: TopicFeedUpdated, Payload: FeedUpdatedPayload{
			Title: updated.Title,
			Verb:  FeedVerbBlocked,
		}}); err != nil {
			return Ticket{}, fmt.Errorf("board: append feed.updated: %w", err)
		}
		return updated, nil
	})
}

// CompletionLink names the completed work on GitHub for the persistent "done"
// feed card (08 §7): URL is the web page (a commit under merge-on-main, a pull
// request under the PR gate) and Label the clickable text (abbreviated SHA or
// "#<number>"). Summary is the landed work's full description — the commit
// message, or the PR title + description — rendered as the card's expandable body
// (preview + tap-to-expand). The brain fills it from the merge-gate verify it just
// ran; both link fields empty means no link is shown (e.g. repository verification
// was unavailable — though the done gate refuses that case upstream), and an empty
// Summary means a body-less card.
type CompletionLink struct {
	URL     string
	Label   string
	Summary string
}

// AcceptToDone moves working|blocked → done, clearing the worker binding
// and blocked_reason and recording doneCommit (03 §4).
// Precondition: state ∈ {working, blocked}, and doneCommit — when non-empty —
// must not already be linked to another ticket (ErrCommitAlreadyUsed), so one
// commit maps to at most one ticket. An empty doneCommit records no commit
// (direct board callers; the brain's merge gate always supplies one, 06 §7).
// Emits pull.evaluate, agent.release (recycle the freed worker — 05 §4),
// feed.updated, the ephemeral finished activity.toast, and feed.completion
// (the persistent "done" card). link is the GitHub reference to the accepted
// work, carried onto the completion card so the feed can link to the commit or
// pull request that landed it.
func (s *Service) AcceptToDone(
	ctx context.Context, projectID string, id TicketID, link CompletionLink, doneCommit string,
) (Ticket, error) {
	return s.mutate(ctx, projectID, "accept_to_done", id, func(ctx context.Context, tx Tx, t *Ticket) (Ticket, error) {
		if !t.State.Active() || t.WorkerID == nil {
			return Ticket{}, &ErrInvalidTransition{From: t.State, Attempted: "AcceptToDone"}
		}
		if doneCommit != "" {
			if err := recordDoneCommit(ctx, tx, projectID, id, doneCommit, t); err != nil {
				return Ticket{}, err
			}
		}
		worker := *t.WorkerID
		t.State = StateDone
		t.WorkerID = nil
		t.BlockedReason = nil
		updated, err := tx.UpdateTicket(ctx, projectID, *t)
		if err != nil {
			return Ticket{}, fmt.Errorf("board: update ticket: %w", err)
		}
		// Emitted in order: pull.evaluate, agent.release (recycle the freed
		// worker), feed.updated, the ephemeral finished toast, the persistent
		// completion card (so a done is never missed when the agent forgets — 08
		// §7), and notify.send (done is one of the three pushed transitions, 02 §10).
		emissions := []Emission{
			{Topic: TopicPullEvaluate},
			{Topic: TopicAgentRelease, Payload: ReleasePayload{WorkerID: worker}},
			{Topic: TopicFeedUpdated, Payload: FeedUpdatedPayload{Title: updated.Title, Verb: FeedVerbFinished}},
			{Topic: TopicActivityToast, Payload: ToastPayload{
				Verb: "finished", TicketID: updated.ID, TicketTitle: updated.Title,
			}},
			{Topic: TopicFeedCompletion, Payload: CompletionPayload{
				TicketID: updated.ID, TicketTitle: updated.Title,
				GitHubURL: link.URL, GitHubLabel: link.Label, Summary: link.Summary,
			}},
			{Topic: TopicNotifySend, Payload: NotifyPayload{
				TicketID: updated.ID, Title: updated.Title, Reason: notifyReasonDone,
			}},
		}
		for _, e := range emissions {
			if err := tx.AppendOutbox(ctx, projectID, e); err != nil {
				return Ticket{}, fmt.Errorf("board: append %s: %w", e.Topic, err)
			}
		}
		return updated, nil
	})
}

// recordDoneCommit enforces the one-commit-to-one-ticket rule and stamps the SHA
// onto t. Lock-then-check (03 §6): the target ticket is already locked, so a
// commit unspent here cannot be spent by a committed sibling; the partial unique
// index (0010) backstops the residual race. Returns ErrCommitAlreadyUsed when
// the commit is already linked to a different ticket.
func recordDoneCommit(ctx context.Context, tx Tx, projectID string, id TicketID, doneCommit string, t *Ticket) error {
	other, ok, err := tx.TicketIDByDoneCommit(ctx, projectID, doneCommit)
	if err != nil {
		return fmt.Errorf("board: lookup done_commit: %w", err)
	}
	if ok && other != id {
		return &ErrCommitAlreadyUsed{SHA: doneCommit, OtherID: other}
	}
	t.DoneCommit = &doneCommit
	return nil
}

// SeedSpec describes a ticket to plant directly into a target state, bypassing
// the brain's create/shape decisions (08 §B.6). Dev/e2e only.
type SeedSpec struct {
	Title             string // required, non-empty (ErrEmptyTitle otherwise)
	Body              string
	State             State  // target state; empty means shaping (the default)
	BlockedReason     string // used only when State == blocked; defaulted if empty
	ApprovalRequested bool   // only meaningful (and only allowed) when State == shaping
}

// SeedTicket plants a ticket directly into SeedSpec.State — DEV/E2E ONLY (08
// §B.6). It exists so an e2e can establish a feed precondition (a blocker card,
// a proposal card) deterministically, without depending on brain/LLM
// discretion. It is not a Board API operation and is never reachable by the
// real client.
//
// Invariants are honored: a working/blocked seed binds one of the project's
// currently-free workers (ErrNoFreeWorker if none), a blocked seed carries a
// blocked_reason (03 I3/I4), a ready seed gets a ready_at, and
// approval_requested is only set on a shaping seed. Emits board.updated
// always, plus feed.updated when the seed produces a feed surface (blocker or
// proposal), so the feed reassembles immediately.
func (s *Service) SeedTicket(ctx context.Context, projectID string, spec SeedSpec) (Ticket, error) {
	if spec.Title == "" {
		return Ticket{}, ErrEmptyTitle
	}
	state := spec.State
	if state == "" {
		state = StateShaping
	}
	var out Ticket
	err := s.store.Tx(ctx, func(tx Tx) error {
		seed, err := buildSeedTicket(ctx, tx, projectID, spec, state)
		if err != nil {
			return err
		}
		created, err := tx.InsertTicket(ctx, projectID, seed)
		if err != nil {
			return fmt.Errorf("board: seed insert ticket: %w", err)
		}
		if err := tx.AppendOutbox(ctx, projectID, Emission{Topic: TopicBoardUpdated}); err != nil {
			return fmt.Errorf("board: append board.updated: %w", err)
		}
		if err := appendSeedFeedUpdated(ctx, tx, projectID, state, created.Title); err != nil {
			return err
		}
		out = created
		return nil
	})
	if err != nil {
		return Ticket{}, fmt.Errorf("board: seed ticket: %w", err)
	}
	logTransition(ctx, "seed_ticket", projectID, string(out.ID), "", out.State)
	return out, nil
}

// appendSeedFeedUpdated emits the seed's feed.updated when the seeded state is a
// feed surface — a blocker card (blocked) or a proposal card (any shaping ticket
// — 08 §5, superseding D5's approval_requested gate) — so the feed reassembles
// immediately. Other states have no feed surface and emit nothing here.
func appendSeedFeedUpdated(ctx context.Context, tx Tx, projectID string, state State, title string) error {
	if state != StateBlocked && state != StateShaping {
		return nil // ready/working/done have no feed surface here
	}
	verb := FeedVerbProposal
	if state == StateBlocked {
		verb = FeedVerbBlocked
	}
	if err := tx.AppendOutbox(ctx, projectID, Emission{Topic: TopicFeedUpdated, Payload: FeedUpdatedPayload{
		Title: title,
		Verb:  verb,
	}}); err != nil {
		return fmt.Errorf("board: append feed.updated: %w", err)
	}
	return nil
}

// buildSeedTicket assembles the invariant-honoring Ticket for SeedTicket: a
// worker binding for active states (03 I3), a blocked_reason for blocked
// (03 I4), a ready_at for ready, and approval_requested only for shaping.
func buildSeedTicket(ctx context.Context, tx Tx, projectID string, spec SeedSpec, state State) (Ticket, error) {
	seed := Ticket{Title: spec.Title, Body: spec.Body, State: state}
	if state.Active() { // working/blocked need a bound worker (03 I3)
		w, ok, err := tx.FreeWorker(ctx, projectID)
		if err != nil {
			return Ticket{}, fmt.Errorf("board: seed free worker: %w", err)
		}
		if !ok {
			return Ticket{}, ErrNoFreeWorker
		}
		wid := w.ID
		seed.WorkerID = &wid
	}
	if state == StateBlocked { // 03 I4
		reason := spec.BlockedReason
		if reason == "" {
			reason = "seeded blocker"
		}
		seed.BlockedReason = &reason
	}
	if state == StateReady {
		now := time.Now().UTC()
		seed.ReadyAt = &now
	}
	seed.ApprovalRequested = state == StateShaping && spec.ApprovalRequested
	return seed, nil
}

// ArchiveTicket soft-deletes a ticket — the brain's delete_ticket (06 §4
// amended). The row is retained (ArchivedAt is stamped) but the ticket
// disappears from every read (Snapshot, GetTicket) and from the pull, and
// every later targeted operation treats it as ErrNotFound.
//
// Precondition: state ∈ {shaping, ready, blocked, done}. Only a *working*
// ticket is refused: it has a live agent mid-turn, so archiving it would kill
// in-flight work — resolve it first (ErrInvalidTransition).
//
//   - shaping/ready/done — a non-active ticket holds no worker; just stamp
//     ArchivedAt and drop the card.
//   - blocked — the ticket is stalled by definition (waiting on a human), so it
//     is the one active state that can be deleted directly
//     (2026-07-11-delete-blocked-ticket-design.md). Archiving it releases the
//     worker it holds: null WorkerID and emit agent.release (tear the sandbox
//     down, 05 §4) + pull.evaluate (let a waiting ready ticket claim the freed
//     slot) — mirroring AcceptToDone's release. Archive is orthogonal to State,
//     so the row keeps state=blocked and its BlockedReason as historical truth;
//     the blocked-with-no-worker shape is permitted because I3 is scoped to live
//     rows (migration 0011).
//
// Emits board.updated (via mutate) and feed.updated (an archived
// proposal/blocker card disappears), plus agent.release + pull.evaluate when a
// blocked ticket is archived.
func (s *Service) ArchiveTicket(ctx context.Context, projectID string, id TicketID) (Ticket, error) {
	return s.mutate(ctx, projectID, "archive_ticket", id, func(ctx context.Context, tx Tx, t *Ticket) (Ticket, error) {
		if t.State == StateWorking {
			return Ticket{}, &ErrInvalidTransition{From: t.State, Attempted: "ArchiveTicket"}
		}
		var emissions []Emission
		// Blocked: free the worker the ticket holds before it leaves the board.
		// The nil guard is defensive — I3 guarantees a blocked ticket binds a
		// worker — but avoids a panic if the invariant is ever violated.
		if t.State == StateBlocked && t.WorkerID != nil {
			worker := *t.WorkerID
			t.WorkerID = nil
			emissions = append(emissions,
				Emission{Topic: TopicAgentRelease, Payload: ReleasePayload{WorkerID: worker}},
				Emission{Topic: TopicPullEvaluate},
			)
		}
		now := time.Now().UTC()
		t.ArchivedAt = &now
		updated, err := tx.UpdateTicket(ctx, projectID, *t)
		if err != nil {
			return Ticket{}, fmt.Errorf("board: update ticket: %w", err)
		}
		emissions = append(emissions, Emission{Topic: TopicFeedUpdated, Payload: FeedUpdatedPayload{
			Title: updated.Title,
			Verb:  FeedVerbArchived,
		}})
		for _, e := range emissions {
			if err := tx.AppendOutbox(ctx, projectID, e); err != nil {
				return Ticket{}, fmt.Errorf("board: append %s: %w", e.Topic, err)
			}
		}
		return updated, nil
	})
}

// GetTicket reads one of the project's tickets by id (03 §4 amended), backing
// the brain's get_ticket tool. Archived, missing, or other-project ids
// surface as ErrNotFound.
func (s *Service) GetTicket(ctx context.Context, projectID string, id TicketID) (Ticket, error) {
	t, err := s.store.GetTicket(ctx, projectID, id)
	if err != nil {
		return Ticket{}, fmt.Errorf("board: get ticket %s: %w", id, err)
	}
	return t, nil
}

// GetBoard returns the project's full snapshot (03 §4).
func (s *Service) GetBoard(ctx context.Context, projectID string) (Snapshot, error) {
	snap, err := s.store.Snapshot(ctx, projectID)
	if err != nil {
		return Snapshot{}, fmt.Errorf("board: get board: %w", err)
	}
	return snap, nil
}

// SetWorkerHealth reconciles the project's worker health so the pull binds only
// healthy sandboxes (03 §5 amended). erroredWorkerIDs are the workers currently
// in a terminal failure state; every other worker of the project is set healthy.
// A system action driven by the agent liveness reconciler, never a brain
// decision.
func (s *Service) SetWorkerHealth(ctx context.Context, projectID string, erroredWorkerIDs []string) error {
	if err := s.store.SetWorkerHealth(ctx, projectID, erroredWorkerIDs); err != nil {
		return fmt.Errorf("board: set worker health: %w", err)
	}
	return nil
}

// RunPull is the deterministic pull (03 §5) — a system action, never a brain
// decision (03 I6). It works one project's board: it loops, one transaction
// per binding (pullOnce), until no (ready ticket, free worker) pair remains
// within the project: lock both with SKIP LOCKED, move ready → working, bind
// the worker, emit agent.send with the work order. Idempotent by
// construction, so duplicate pull.evaluate triggers and at-least-once
// delivery are safe; the one_active_ticket_per_worker index (03 I2) is the
// backstop against double-binding.
func (s *Service) RunPull(ctx context.Context, projectID string) error {
	for {
		bound, err := s.pullOnce(ctx, projectID)
		if err != nil {
			return err
		}
		if !bound {
			return nil
		}
	}
}

// pullOnce binds at most one of the project's (ready ticket, free worker)
// pairs in a single transaction, reporting whether a binding happened. When
// either side is exhausted it commits an empty transaction and reports
// bound=false, which stops RunPull's loop.
func (s *Service) pullOnce(ctx context.Context, projectID string) (bool, error) {
	bound := false
	var boundTicket, boundWorker string
	err := s.store.Tx(ctx, func(tx Tx) error {
		ticket, ok, err := tx.NextReadyTicket(ctx, projectID)
		if err != nil {
			return fmt.Errorf("board: next ready ticket: %w", err)
		}
		if !ok {
			return nil
		}
		worker, ok, err := tx.FreeWorker(ctx, projectID)
		if err != nil {
			return fmt.Errorf("board: free worker: %w", err)
		}
		if !ok {
			return nil
		}
		wid := worker.ID
		ticket.State = StateWorking
		ticket.WorkerID = &wid
		updated, err := tx.UpdateTicket(ctx, projectID, ticket)
		if err != nil {
			return fmt.Errorf("board: update ticket: %w", err)
		}
		if err := emitPullEffects(ctx, tx, projectID, updated, wid); err != nil {
			return err
		}
		bound = true
		boundTicket = string(updated.ID)
		boundWorker = string(wid)
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("board: run pull: %w", err)
	}
	logPull(ctx, projectID, bound, boundTicket, boundWorker)
	return bound, nil
}

// emitPullEffects appends the outbox rows for one binding (03 §7.1): the
// agent.send work order, the universal board.updated, the in-app "started"
// toast, and the start push (notify.send — start is one of the three
// transitions the user is pushed for, 02 §10; the toast only surfaces in-app).
// Split out of pullOnce so the pull's control flow stays within the complexity
// budget.
func emitPullEffects(ctx context.Context, tx Tx, projectID string, t Ticket, wid WorkerID) error {
	if err := tx.AppendOutbox(ctx, projectID, Emission{Topic: TopicAgentSend, Payload: SendPayload{
		TicketID: t.ID,
		WorkerID: wid,
		Message:  workOrder(t),
	}}); err != nil {
		return fmt.Errorf("board: append agent.send: %w", err)
	}
	if err := tx.AppendOutbox(ctx, projectID, Emission{Topic: TopicBoardUpdated}); err != nil {
		return fmt.Errorf("board: append board.updated: %w", err)
	}
	if err := tx.AppendOutbox(ctx, projectID, Emission{Topic: TopicActivityToast, Payload: ToastPayload{
		Verb:        "started",
		TicketID:    t.ID,
		TicketTitle: t.Title,
	}}); err != nil {
		return fmt.Errorf("board: append activity.toast: %w", err)
	}
	if err := tx.AppendOutbox(ctx, projectID, Emission{Topic: TopicNotifySend, Payload: NotifyPayload{
		TicketID: t.ID,
		Title:    t.Title,
		Reason:   notifyReasonStarted,
	}}); err != nil {
		return fmt.Errorf("board: append notify.send: %w", err)
	}
	return nil
}

// logPull emits the pull's ready→working board.transition when a binding
// happened. The system pull is the one state change that is never a brain tool
// (03 I6); logging it keeps every ready→working move — including an automatic
// redelivery — in the same board.transition stream. Split out of pullOnce so
// the pull's own control flow stays within the complexity budget.
func logPull(ctx context.Context, projectID string, bound bool, ticketID, workerID string) {
	if !bound {
		return
	}
	slog.InfoContext(ctx, "board.transition",
		"op", "pull", "project_id", projectID, "ticket_id", ticketID, "worker_id", workerID,
		"from", string(StateReady), "to", string(StateWorking))
}

// mutate runs the common lock-then-check transaction shape (03 §6): lock the
// target ticket within the project, hand it to apply for precondition-check +
// state change + emissions, then append the universal board.updated signal
// (03 §4). apply returns the persisted ticket; any error rolls back the whole
// transaction so no partial write or emission survives a failed precondition
// (03 I7, D8).
//
// op names the Board API operation for the board.transition log emitted on
// commit — the authoritative before/after state record (turn_id injected from
// context), covering every operation that flows through here.
func (s *Service) mutate(
	ctx context.Context,
	projectID string,
	op string,
	id TicketID,
	apply func(ctx context.Context, tx Tx, t *Ticket) (Ticket, error),
) (Ticket, error) {
	var out Ticket
	var before State
	err := s.store.Tx(ctx, func(tx Tx) error {
		locked, err := tx.LockTicket(ctx, projectID, id)
		if err != nil {
			return fmt.Errorf("board: lock ticket %s: %w", id, err)
		}
		before = locked.State
		updated, err := apply(ctx, tx, &locked)
		if err != nil {
			return err
		}
		if err := tx.AppendOutbox(ctx, projectID, Emission{Topic: TopicBoardUpdated}); err != nil {
			return fmt.Errorf("board: append board.updated: %w", err)
		}
		out = updated
		return nil
	})
	if err != nil {
		return Ticket{}, fmt.Errorf("board: mutate ticket %s: %w", id, err)
	}
	logTransition(ctx, op, projectID, string(id), before, out.State)
	return out, nil
}

// logTransition emits the structured board.transition record (before/after
// state + project + ticket id) once a mutation has committed. Shared by
// mutate, the create/seed inserts (from ""), and the pull (op="pull") so
// every state change — brain-driven or the system pull — appears in one
// greppable log stream.
func logTransition(ctx context.Context, op, projectID, ticketID string, from, to State) {
	slog.InfoContext(ctx, "board.transition",
		"op", op, "project_id", projectID, "ticket_id", ticketID, "from", string(from), "to", string(to))
}

// workOrder is RunPull's agent.send message — the ticket's title and body as
// the work instruction (03 §7.1). SendToAgent supplies its own instruction
// instead; the agent-runtime module derives first-message-vs-continuation.
func workOrder(t Ticket) string {
	if t.Body == "" {
		return t.Title
	}
	return t.Title + "\n\n" + t.Body
}
