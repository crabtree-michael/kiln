package main

// Port adapters — the composition root's core job (02 §2, 04 §8 D9): every
// internal/* module depends only on the narrow ports it declares, never on a
// sibling module's concrete type, so this is the only file where board.Service,
// brain.Service, agent.Service, and runtime.Service meet.
//
// Under multi-tenancy (11 §3) the adapters split into two flavours:
//
//   - Singleton adapters, built once, whose port methods now carry a projectID
//     (or a userID for the push/notification paths) so one shared service
//     instance serves every tenant — the runtime dispatcher, the steward, the
//     api hub, and the agent runtime all thread the tenant id through.
//   - Per-project adapters, built fresh inside the tenant.Registry closure for
//     each project (wiring.go's buildTenantProviders): they close over the
//     resolved projectID and inject it into every board/runtime/agent call, so
//     the tenancy-unaware brain always operates against exactly one project.
//
// Two of these adapters break construction cycles: brainAdapter.inner and
// agentEventAdapter.rt are filled after the service they point at is built.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
	"github.com/crabtree-michael/kiln/backend/internal/api"
	"github.com/crabtree-michael/kiln/backend/internal/beta"
	"github.com/crabtree-michael/kiln/backend/internal/board"
	"github.com/crabtree-michael/kiln/backend/internal/brain"
	"github.com/crabtree-michael/kiln/backend/internal/push"
	"github.com/crabtree-michael/kiln/backend/internal/repo"
	"github.com/crabtree-michael/kiln/backend/internal/runtime"
	"github.com/crabtree-michael/kiln/backend/internal/steward"
)

// ownerLookup resolves a project to its owning user id (11 §3) — the
// notification path's tenant→recipient hop. Its shape matches runtime.Owner;
// the composition root's ownerResolver (wiring.go, over the tenant registry)
// satisfies both.
type ownerLookup interface {
	Owner(ctx context.Context, projectID string) (string, error)
}

// brainAdapter satisfies runtime.Brain over *brain.Service (04 §2 Brain
// port), mapping runtime.Event → brain.Event field-for-field. Under
// multi-tenancy one brainAdapter wraps each project's own *brain.Service and is
// stored in the project's tenant.Providers.Brain (typed any), then handed back
// by the BrainResolver as the runtime.Brain for that project's events.
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
// the returned Ticket. projectID scopes the lookup so a ticket id can never be
// blocked across tenants (11 §3).
type blockerAdapter struct{ inner *board.Service }

func (a *blockerAdapter) MarkBlocked(ctx context.Context, projectID, ticketID, reason string) error {
	if _, err := a.inner.MarkBlocked(ctx, projectID, board.TicketID(ticketID), reason); err != nil {
		return fmt.Errorf("kiln: mark blocked: %w", err)
	}
	return nil
}

var _ runtime.Blocker = (*blockerAdapter)(nil)

// boardViewAdapter satisfies runtime.BoardReader over *board.Service (08 §3
// feed assembly, 11 §3): map a board.Snapshot to the runtime's local BoardView
// so the runtime never imports internal/board. Blocked tickets become blocker
// cards (BlockedReason dereferenced, "" when nil); every shaping ticket becomes
// a proposal card; the working/blocked counts drive the header summary.
type boardViewAdapter struct{ inner *board.Service }

func (a *boardViewAdapter) BoardView(ctx context.Context, projectID string) (runtime.BoardView, error) {
	snap, err := a.inner.GetBoard(ctx, projectID)
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

// agentRuntimeAdapter satisfies runtime.AgentRuntime over *agent.Service
// (05 §2.1, 11 §3). The runtime hands the claimed entry's projectID alongside
// the outbox id; board's SendPayload/ReleasePayload (internal/board/outbox.go)
// carry no project_id — the board module isn't tenancy-aware — so this adapter
// stamps the runtime-supplied projectID onto the raw payload before handing it
// to *agent.Service, whose Send/Release decode project_id from the payload
// itself (11 §3).
type agentRuntimeAdapter struct{ inner *agent.Service }

func withProjectID(payload []byte, projectID string) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return nil, fmt.Errorf("kiln: decode payload: %w", err)
	}
	stamped, err := json.Marshal(projectID)
	if err != nil {
		return nil, fmt.Errorf("kiln: encode project id: %w", err)
	}
	fields["project_id"] = stamped
	out, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("kiln: encode payload: %w", err)
	}
	return out, nil
}

func (a *agentRuntimeAdapter) Send(ctx context.Context, projectID string, idempotencyKey int64, payload []byte) error {
	payload, err := withProjectID(payload, projectID)
	if err != nil {
		return fmt.Errorf("kiln: agent send: %w", err)
	}
	if err := a.inner.Send(ctx, idempotencyKey, payload); err != nil {
		return fmt.Errorf("kiln: agent send: %w", err)
	}
	return nil
}

func (a *agentRuntimeAdapter) Release(
	ctx context.Context, projectID string, idempotencyKey int64, payload []byte,
) error {
	payload, err := withProjectID(payload, projectID)
	if err != nil {
		return fmt.Errorf("kiln: agent release: %w", err)
	}
	if err := a.inner.Release(ctx, idempotencyKey, payload); err != nil {
		return fmt.Errorf("kiln: agent release: %w", err)
	}
	return nil
}

var _ runtime.AgentRuntime = (*agentRuntimeAdapter)(nil)

// agentEventAdapter satisfies agent.EventEnqueuer over *runtime.Service
// (05 §2.2's inbound seam): thread the emitting turn's projectID (11 §3) and
// wrap the plain-string event type as runtime.EventType. rt is set after
// runtime.Service is constructed, resolving the runtime↔agent cycle.
type agentEventAdapter struct{ rt *runtime.Service }

func (a *agentEventAdapter) EnqueueEvent(
	ctx context.Context, projectID, eventType string, idempotencyKey int64, payload []byte,
) (int64, error) {
	id, err := a.rt.EnqueueEvent(ctx, projectID, runtime.EventType(eventType), idempotencyKey, payload)
	if err != nil {
		return 0, fmt.Errorf("kiln: enqueue event: %w", err)
	}
	return id, nil
}

var _ agent.EventEnqueuer = (*agentEventAdapter)(nil)

// boardAPIAdapter satisfies brain.BoardAPI over *board.Service, injecting the
// project this brain is scoped to (11 §3) into every mutation. Built per project
// in the tenant registry closure — the brain never names a project itself.
//
// The board's typed errors (ErrNotFound, ErrInvalidTransition) must reach the
// model VERBATIM as tool results (brain ports.go, 06 §6/§8) — the brain renders
// err.Error() straight into the tool_result content — so every method returns
// the board error unwrapped (a wrap would leak a "kiln:" prefix to the model).
type boardAPIAdapter struct {
	svc       *board.Service
	projectID string
}

//nolint:wrapcheck // board errors reach the model verbatim (see type doc).
func (a *boardAPIAdapter) CreateTicket(ctx context.Context, title, body string) (board.Ticket, error) {
	return a.svc.CreateTicket(ctx, a.projectID, title, body)
}

//nolint:wrapcheck // board errors reach the model verbatim (see type doc).
func (a *boardAPIAdapter) ShapeTicket(
	ctx context.Context, id board.TicketID, patch board.ShapePatch,
) (board.Ticket, error) {
	return a.svc.ShapeTicket(ctx, a.projectID, id, patch)
}

//nolint:wrapcheck // board errors reach the model verbatim (see type doc).
func (a *boardAPIAdapter) MarkReady(ctx context.Context, id board.TicketID) (board.Ticket, error) {
	return a.svc.MarkReady(ctx, a.projectID, id)
}

//nolint:wrapcheck // board errors reach the model verbatim (see type doc).
func (a *boardAPIAdapter) SendToAgent(
	ctx context.Context, id board.TicketID, instruction string,
) (board.Ticket, error) {
	return a.svc.SendToAgent(ctx, a.projectID, id, instruction)
}

//nolint:wrapcheck // board errors reach the model verbatim (see type doc).
func (a *boardAPIAdapter) MarkBlocked(ctx context.Context, id board.TicketID, reason string) (board.Ticket, error) {
	return a.svc.MarkBlocked(ctx, a.projectID, id, reason)
}

//nolint:wrapcheck // board errors reach the model verbatim (see type doc).
func (a *boardAPIAdapter) AcceptToDone(
	ctx context.Context, id board.TicketID, link board.CompletionLink, doneCommit string,
) (board.Ticket, error) {
	return a.svc.AcceptToDone(ctx, a.projectID, id, link, doneCommit)
}

//nolint:wrapcheck // board errors reach the model verbatim (see type doc).
func (a *boardAPIAdapter) ArchiveTicket(ctx context.Context, id board.TicketID) (board.Ticket, error) {
	return a.svc.ArchiveTicket(ctx, a.projectID, id)
}

//nolint:wrapcheck // board errors reach the model verbatim (see type doc).
func (a *boardAPIAdapter) RequestApproval(ctx context.Context, id board.TicketID) (board.Ticket, error) {
	return a.svc.RequestApproval(ctx, a.projectID, id)
}

var _ brain.BoardAPI = (*boardAPIAdapter)(nil)

// boardReaderAdapter satisfies brain.BoardReader over *board.Service, scoped to
// one project (11 §3). Like boardAPIAdapter, board errors reach the model
// verbatim, so GetTicket's ErrNotFound is returned unwrapped.
type boardReaderAdapter struct {
	svc       *board.Service
	projectID string
}

//nolint:wrapcheck // board errors reach the model verbatim (see type doc).
func (a *boardReaderAdapter) GetBoard(ctx context.Context) (board.Snapshot, error) {
	return a.svc.GetBoard(ctx, a.projectID)
}

//nolint:wrapcheck // board errors reach the model verbatim (see type doc).
func (a *boardReaderAdapter) GetTicket(ctx context.Context, id board.TicketID) (board.Ticket, error) {
	return a.svc.GetTicket(ctx, a.projectID, id)
}

var _ brain.BoardReader = (*boardReaderAdapter)(nil)

// sayAdapter satisfies brain.Say over *runtime.Service.Say, scoped to one
// project (07 §3, 11 §3).
type sayAdapter struct {
	rt        *runtime.Service
	projectID string
}

func (a *sayAdapter) Say(ctx context.Context, text string) error {
	if err := a.rt.Say(ctx, a.projectID, text); err != nil {
		return fmt.Errorf("kiln: say: %w", err)
	}
	return nil
}

var _ brain.Say = (*sayAdapter)(nil)

// notificationsAdapter satisfies brain.NotificationStore over *runtime.Service
// (08 §7), scoped to one project (11 §3). *runtime.Service no longer satisfies
// the port directly because its notification writes gained a leading projectID.
type notificationsAdapter struct {
	rt        *runtime.Service
	projectID string
}

func (a *notificationsAdapter) PostNotification(
	ctx context.Context, kind, body string, ticketID, imageURL *string,
) error {
	if err := a.rt.PostNotification(ctx, a.projectID, kind, body, ticketID, imageURL); err != nil {
		return fmt.Errorf("kiln: post notification: %w", err)
	}
	return nil
}

func (a *notificationsAdapter) RetractNotification(ctx context.Context, id int64) error {
	if err := a.rt.RetractNotification(ctx, a.projectID, id); err != nil {
		return fmt.Errorf("kiln: retract notification: %w", err)
	}
	return nil
}

func (a *notificationsAdapter) EditNotification(
	ctx context.Context, id int64, kind, body string, imageURL *string,
) error {
	if err := a.rt.EditNotification(ctx, a.projectID, id, kind, body, imageURL); err != nil {
		return fmt.Errorf("kiln: edit notification: %w", err)
	}
	return nil
}

var _ brain.NotificationStore = (*notificationsAdapter)(nil)

// convoAdapter satisfies brain.ConversationReader over *runtime.Service
// (06 §3.2, 07 §3): call rt.Recent for this project and map
// runtime.Message → brain.Message.
type convoAdapter struct {
	rt        *runtime.Service
	projectID string
}

func (a *convoAdapter) Recent(ctx context.Context, n int) ([]brain.Message, error) {
	msgs, err := a.rt.Recent(ctx, a.projectID, n)
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
// amended, 08 §7): call rt.ListNotifications for this project and map
// runtime.Notification → brain.Update.
type feedReaderAdapter struct {
	rt        *runtime.Service
	projectID string
}

func (a *feedReaderAdapter) ListUpdates(ctx context.Context) ([]brain.Update, error) {
	notes, err := a.rt.ListNotifications(ctx, a.projectID)
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
// value-copies (brain cannot import internal/agent). Scoped to one project
// (11 §3); no provider handle crosses.
type agentInspectorAdapter struct {
	inner     *agent.Service
	projectID string
}

func (a *agentInspectorAdapter) ListAgents(ctx context.Context) ([]brain.AgentInfo, error) {
	infos, err := a.inner.ListAgents(ctx, a.projectID)
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
	u, err := a.inner.GetAgentUpdates(ctx, a.projectID, workerID)
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
// resolving the api-hub↔agent cycle. The hub asks per project (11 §3) — the
// board snapshot it joins statuses onto is already project-scoped. No provider
// handle crosses.
type agentStatusAdapter struct{ inner *agent.Service }

func (a *agentStatusAdapter) ListAgents(ctx context.Context, projectID string) ([]api.AgentInfo, error) {
	infos, err := a.inner.ListAgents(ctx, projectID)
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
// changes a worker's status (amended 2026-07-05). The liveness sweep is
// cross-project (05 §4, 11 §3), so the single RefreshBoard nudge fans a fresh
// snapshot out for every known project; the hub's broadcast is partitioned by
// project, so only clients of a project with a real change actually receive one.
type boardRefreshAdapter struct {
	hub      *api.Hub
	projects projectLister
	health   workerHealthSetter
}

func (a *boardRefreshAdapter) RefreshBoard(ctx context.Context) error {
	ids, err := a.projects.ProjectIDs(ctx)
	if err != nil {
		return fmt.Errorf("kiln: refresh board list projects: %w", err)
	}
	for _, pid := range ids {
		if err := a.hub.PushBoard(ctx, pid); err != nil {
			return fmt.Errorf("kiln: refresh board %s: %w", pid, err)
		}
	}
	return nil
}

// SetWorkerHealth forwards the agent liveness loop's per-project errored-worker
// set to the board so the pull skips failing sandboxes (03 §5 amended). The
// board owns the workers row; this adapter only bridges the agent module's
// outbound seam to it.
func (a *boardRefreshAdapter) SetWorkerHealth(ctx context.Context, projectID string, erroredWorkerIDs []string) error {
	if err := a.health.SetWorkerHealth(ctx, projectID, erroredWorkerIDs); err != nil {
		return fmt.Errorf("kiln: set worker health %s: %w", projectID, err)
	}
	return nil
}

// workerHealthSetter is the board write the boardRefreshAdapter needs; satisfied
// by *board.Service.
type workerHealthSetter interface {
	SetWorkerHealth(ctx context.Context, projectID string, erroredWorkerIDs []string) error
}

var _ agent.BoardRefresher = (*boardRefreshAdapter)(nil)

// projectLister is the read of the live project set the cross-project
// singletons (the board-refresh nudge here) enumerate. Satisfied by the
// composition root's projectsResolver over identity.
type projectLister interface {
	ProjectIDs(ctx context.Context) ([]string, error)
}

// stewardBoardAdapter satisfies steward.Board over *board.Service, scoped per
// project (11 §3): the Working set (each ticket with its bound worker slot), a
// poke (a Working→Working new turn), and the escalation to Blocked.
type stewardBoardAdapter struct{ inner *board.Service }

func (a *stewardBoardAdapter) WorkingTickets(ctx context.Context, projectID string) ([]steward.WorkingTicket, error) {
	snap, err := a.inner.GetBoard(ctx, projectID)
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

func (a *stewardBoardAdapter) Poke(ctx context.Context, projectID, ticketID string) error {
	if _, err := a.inner.SendToAgent(ctx, projectID, board.TicketID(ticketID), "poke"); err != nil {
		return fmt.Errorf("kiln: steward poke: %w", err)
	}
	return nil
}

func (a *stewardBoardAdapter) Block(ctx context.Context, projectID, ticketID, reason string) error {
	if _, err := a.inner.MarkBlocked(ctx, projectID, board.TicketID(ticketID), reason); err != nil {
		return fmt.Errorf("kiln: steward block: %w", err)
	}
	return nil
}

var _ steward.Board = (*stewardBoardAdapter)(nil)

// stewardAgentAdapter satisfies steward.Agents over *agent.Service.ListAgents:
// each live worker's neutral status + last-activity time, keyed by worker slot
// id, for one project (11 §3). No provider handle crosses.
type stewardAgentAdapter struct{ inner *agent.Service }

func (a *stewardAgentAdapter) States(ctx context.Context, projectID string) (map[string]steward.AgentState, error) {
	infos, err := a.inner.ListAgents(ctx, projectID)
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
// the feed-only poke card for one project (ticket title + 👉, no body).
type stewardFeedAdapter struct{ inner *runtime.Service }

func (a *stewardFeedAdapter) PostPoke(ctx context.Context, projectID, ticketID string) error {
	if err := a.inner.PostPoke(ctx, projectID, ticketID); err != nil {
		return fmt.Errorf("kiln: steward post poke: %w", err)
	}
	return nil
}

var _ steward.Feed = (*stewardFeedAdapter)(nil)

// repoShellAdapter bridges *repo.Shell to brain.RepoShell, converting the
// repo module's Result to the brain's own value-copy (brain cannot import
// internal/repo). Built per project in the tenant registry closure over that
// project's own clone. *repo.Shell.Run never errors, so this always returns nil.
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

// VerifyOnMain converts repo.Verify → brain.RepoVerify. Like Run it never errors
// (best-effort), so the error is always nil.
func (a *repoShellAdapter) VerifyOnMain(ctx context.Context, sha string) (brain.RepoVerify, error) {
	v := a.inner.VerifyOnMain(ctx, sha)
	return brain.RepoVerify{
		OnMain:      v.OnMain,
		URL:         v.URL,
		Ref:         v.Ref,
		Unavailable: v.Unavailable,
		Reason:      v.Reason,
	}, nil
}

// VerifyInPR converts repo.Verify → brain.RepoVerify for the pull-request gate
// mode. Best-effort like VerifyOnMain, so the error is always nil.
func (a *repoShellAdapter) VerifyInPR(ctx context.Context, sha string) (brain.RepoVerify, error) {
	v := a.inner.VerifyInPR(ctx, sha)
	return brain.RepoVerify{
		InPR:        v.InPR,
		URL:         v.URL,
		Ref:         v.Ref,
		Unavailable: v.Unavailable,
		Reason:      v.Reason,
	}, nil
}

var _ brain.RepoShell = (*repoShellAdapter)(nil)

// workerLister is the concrete board capacity-slot read the composition root
// backs agent.Slots with (05 §4), scoped by projectID (11 §3). Satisfied by
// *board/postgres.Store's WorkerIDs.
type workerLister interface {
	WorkerIDs(ctx context.Context, projectID string) ([]string, error)
}

// slotsAdapter satisfies agent.Slots over the board store's WorkerIDs read.
type slotsAdapter struct{ store workerLister }

func (a *slotsAdapter) WorkerIDs(ctx context.Context, projectID string) ([]string, error) {
	ids, err := a.store.WorkerIDs(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("kiln: list worker ids: %w", err)
	}
	return ids, nil
}

var _ agent.Slots = (*slotsAdapter)(nil)

// logNotifier satisfies runtime.Notifier (02 §10 executor for notify.send).
// It is the fallback when no VAPID key pair is configured — a structured log
// line, no real push. projectID is logged for tenant traceability but the
// log-only path targets no subscriptions.
type logNotifier struct{ log *slog.Logger }

func (n *logNotifier) Send(_ context.Context, projectID string, payload []byte) error {
	n.log.Info("notify.send", "project_id", projectID, "payload", string(payload))
	return nil
}

var _ runtime.Notifier = (*logNotifier)(nil)

// webPushNotifier satisfies runtime.Notifier by delivering real Web Push
// messages (02 §10, 11 §3). It resolves the project's owner (push subscriptions
// hang off a user, not a project), decodes the notify.send payload — a
// board.NotifyPayload snapshot (03 §7.1) — into the user-facing Notification,
// and hands it to the push.Sender scoped to that owner. A project whose owner
// cannot be resolved refuses to send (a misdirected push is worse than a
// dropped one).
type webPushNotifier struct {
	sender *push.Sender
	owner  ownerLookup
}

func (n *webPushNotifier) Send(ctx context.Context, projectID string, payload []byte) error {
	userID, err := n.owner.Owner(ctx, projectID)
	if err != nil {
		return fmt.Errorf("kiln: web push resolve owner: %w", err)
	}
	var p board.NotifyPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("kiln: decode notify payload: %w", err)
	}
	if err := n.sender.Send(ctx, userID, push.Notification{
		Title: p.Title,
		Body:  p.Reason,
		URL:   notifyURL(p.TicketID),
	}); err != nil {
		return fmt.Errorf("kiln: web push send: %w", err)
	}
	return nil
}

var _ runtime.Notifier = (*webPushNotifier)(nil)

// notifyURL is the tap-to-open deep link for a notify.send (02 §10):
// "/app?ticket=<id>", which the frontend reads on load (or via the service
// worker's postMessage) to open that ticket's detail overlay. The primary screen
// lives at "/app" ("/" is the marketing landing page), so a payload with no
// ticket falls back to the plain app root.
func notifyURL(id board.TicketID) string {
	if id == "" {
		return "/app"
	}
	return "/app?" + url.Values{"ticket": {string(id)}}.Encode()
}

// pushRegistrarAdapter satisfies api.PushRegistrar over the push store (02 §10,
// 11 §3): the client's POST /api/push/subscribe lands a browser subscription
// under the signed-in user that webPushNotifier later reads (wire.PushSubscription
// → push.Subscription), and GET/PUT /api/push/mode read/write that user's
// notification frequency. Every method is scoped by userID — the push routes are
// withSession-guarded (per-user, not per-project). The mode no longer gates the
// runtime's pushes — only genuine ticket state transitions notify, regardless of
// mode (design 2026-07-07) — but the endpoint is retained so the existing
// Settings toggle keeps working.
type pushRegistrarAdapter struct{ store push.Store }

func (a *pushRegistrarAdapter) Subscribe(ctx context.Context, userID string, sub api.PushSubscription) error {
	if err := a.store.Save(ctx, userID, push.Subscription{
		Endpoint: sub.Endpoint,
		P256dh:   sub.P256dh,
		Auth:     sub.Auth,
	}); err != nil {
		return fmt.Errorf("kiln: save push subscription: %w", err)
	}
	return nil
}

func (a *pushRegistrarAdapter) Unsubscribe(ctx context.Context, userID, endpoint string) error {
	if err := a.store.DeleteUserEndpoint(ctx, userID, endpoint); err != nil {
		return fmt.Errorf("kiln: delete push subscription: %w", err)
	}
	return nil
}

func (a *pushRegistrarAdapter) Mode(ctx context.Context, userID string) (string, error) {
	mode, err := a.store.Mode(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("kiln: read push mode: %w", err)
	}
	return mode, nil
}

func (a *pushRegistrarAdapter) SetMode(ctx context.Context, userID, mode string) error {
	if err := a.store.SetMode(ctx, userID, mode); err != nil {
		return fmt.Errorf("kiln: set push mode: %w", err)
	}
	return nil
}

var _ api.PushRegistrar = (*pushRegistrarAdapter)(nil)

// betaRegistrarAdapter satisfies api.BetaRegistrar over the beta store: the
// landing page's POST /api/beta-signup lands one email on the beta_signups
// table. Idempotent on the address (the store swallows a duplicate).
type betaRegistrarAdapter struct{ store beta.Store }

func (a *betaRegistrarAdapter) Register(ctx context.Context, email string) error {
	if err := a.store.Save(ctx, email); err != nil {
		return fmt.Errorf("kiln: save beta signup: %w", err)
	}
	return nil
}

var _ api.BetaRegistrar = (*betaRegistrarAdapter)(nil)

// newNotifier chooses the notify.send executor (02 §10): a real Web Push sender
// when the operator has supplied a VAPID key pair (VAPID_PUBLIC_KEY +
// VAPID_PRIVATE_KEY), otherwise the log-only fallback. The web-push path needs
// an owner resolver (projectID → owner userID) to target the right user's
// subscriptions (11 §3). Both VAPID keys are required; a lone key cannot sign,
// so a partial config falls back to logging with a warning.
func newNotifier(cfg Config, store push.Store, owner ownerLookup, log *slog.Logger) runtime.Notifier {
	switch {
	case cfg.VAPIDPublicKey != "" && cfg.VAPIDPrivateKey != "":
		log.Info("notify.send: web push enabled")
		return &webPushNotifier{
			sender: push.NewSender(store, cfg.VAPIDPublicKey, cfg.VAPIDPrivateKey, cfg.VAPIDSubject, nil, log),
			owner:  owner,
		}
	case cfg.VAPIDPublicKey != "" || cfg.VAPIDPrivateKey != "":
		log.Warn("notify.send: partial VAPID config (need both public and private key) — falling back to log-only")
		return &logNotifier{log: log}
	default:
		return &logNotifier{log: log}
	}
}

// realClock is the wall-clock Clock runtime (04 §9), agent (05 §10) and steward
// all want, satisfying every port with the same value.
type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

var (
	_ runtime.Clock = realClock{}
	_ agent.Clock   = realClock{}
	_ steward.Clock = realClock{}
)

// The api SSE hub satisfies every runtime push port (04/07/08), each now
// carrying the projectID the event belongs to (11 §3). The assertions live here
// at the composition root — the api package deliberately dropped them from
// hub.go so its own tests stay decoupled from this cross-module tenancy change.
var (
	_ runtime.SnapshotPusher = (*api.Hub)(nil)
	_ runtime.SayPusher      = (*api.Hub)(nil)
	_ runtime.FeedPusher     = (*api.Hub)(nil)
	_ runtime.ActivityPusher = (*api.Hub)(nil)
)

// resetCoordinator satisfies api.Resetter (docs/superpowers/specs/
// 2026-07-04-debug-reset-session-design.md): the developer "fresh session"
// reset the /debug client fires, now scoped to one project (11 §3). It spans two
// modules — a DB state truncate and the agent service's worker teardown —
// neither of which owns the other's state, so it lives at the composition root.
type resetCoordinator struct {
	state           projectStateDeleter
	workers         workerResetter
	pool            poolReconciler
	defaultPoolSize int
	// workerCountFor resolves a project's configured worker count so a reset
	// re-seeds the pool to the dashboard setting rather than the deployment
	// default; nil when identity is unconfigured, in which case defaultPoolSize
	// (the global KILN_WORKER_COUNT default) is used as the fallback.
	workerCountFor func(ctx context.Context, projectID string) (int, error)
}

// newResetCoordinator wires the reset over the shared pool, the agent service,
// and the board's worker-pool store.
func newResetCoordinator(
	db *sql.DB, workers workerResetter, pool poolReconciler, poolSize int,
	workerCountFor func(ctx context.Context, projectID string) (int, error),
) *resetCoordinator {
	return &resetCoordinator{
		state:           &dbStateDeleter{db: db},
		workers:         workers,
		pool:            pool,
		defaultPoolSize: poolSize,
		workerCountFor:  workerCountFor,
	}
}

// projectStateDeleter deletes one project's runtime state rows (board,
// transcript, queue, notifications) scoped by project_id; schema_migrations and
// every other tenant's rows are left intact (11 §3).
type projectStateDeleter interface {
	DeleteProjectState(ctx context.Context, projectID string) error
}

// workerResetter tears down one project's live agent sandboxes and clears that
// project's entries from the module's in-memory worker cache. Satisfied directly
// by *agent.Service (its ResetProject is scoped to the caller's project, 11 §3).
type workerResetter interface {
	ResetProject(ctx context.Context, projectID string) error
}

// poolReconciler re-seeds one project's worker-slot pool to n rows — the same
// per-project startup call the tenant registry makes (03 §8, 11 §3). Satisfied
// directly by *boardpg.Store.
type poolReconciler interface {
	ReconcileWorkers(ctx context.Context, projectID string, n int) error
}

// Reset returns the caller's project to a fresh session in three steps, all
// scoped to projectID so a reset never touches another tenant's data (11 §3):
// (1) delete this project's state rows (empties its worker slots so nothing is
// "wanted"); (2) tear down this project's live sandboxes and clear its cached
// handles while its wanted set is empty; (3) re-seed THIS project's worker pool
// so its reconciler provisions a fresh idle pool, exactly as at startup.
func (c *resetCoordinator) Reset(ctx context.Context, projectID string) error {
	if err := c.state.DeleteProjectState(ctx, projectID); err != nil {
		return fmt.Errorf("kiln: reset delete project state: %w", err)
	}
	if err := c.workers.ResetProject(ctx, projectID); err != nil {
		return fmt.Errorf("kiln: reset workers: %w", err)
	}
	if err := c.pool.ReconcileWorkers(ctx, projectID, c.poolSize(ctx, projectID)); err != nil {
		return fmt.Errorf("kiln: reset reconcile worker pool: %w", err)
	}
	return nil
}

// poolSize resolves how many worker slots a fresh session re-seeds to: the
// project's configured worker count when identity can supply it, else the
// deployment default (KILN_WORKER_COUNT). A resolver error is non-fatal — the
// reset still brings the project back up with default capacity rather than
// failing outright.
func (c *resetCoordinator) poolSize(ctx context.Context, projectID string) int {
	if c.workerCountFor != nil {
		if n, err := c.workerCountFor(ctx, projectID); err == nil && n > 0 {
			return n
		}
	}
	return c.defaultPoolSize
}

var (
	_ api.Resetter        = (*resetCoordinator)(nil)
	_ workerResetter      = (*agent.Service)(nil)
	_ projectStateDeleter = (*dbStateDeleter)(nil)
)

// dbStateDeleter is the projectStateDeleter over the shared Postgres pool: a
// per-table DELETE ... WHERE project_id = $1 across the eight state tables, so
// only the caller's rows are removed (11 §3). It reuses bootstrap's
// projectIDTables — the same eight tables, in the same order — so the delete set
// can never drift from the adoption set. That order also happens to be FK-safe:
// tickets (which references workers) comes before workers, the only FK edge
// among these tables (agent_turns/steward_pokes carry worker_id by value). No
// RESTART IDENTITY — sequences are shared bigserials that cannot be reset per
// tenant, and ids incrementing across tenants is harmless.
type dbStateDeleter struct{ db *sql.DB }

func (t *dbStateDeleter) DeleteProjectState(ctx context.Context, projectID string) error {
	tx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("kiln: delete project state begin: %w", err)
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			slog.ErrorContext(ctx, "kiln: delete project state rollback", "err", rbErr)
		}
	}()
	for _, table := range projectIDTables {
		// table is a hardcoded name from projectIDTables, never user input.
		//nolint:gosec // G202: constant table name, not attacker-controlled
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE project_id = $1`, projectID); err != nil {
			return fmt.Errorf("kiln: delete project state from %s: %w", table, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("kiln: delete project state commit: %w", err)
	}
	return nil
}
