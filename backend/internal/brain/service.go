package brain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/crabtree-michael/kiln/backend/internal/board"
)

// errMalformedRepeated fails a pass when the model emits malformed output
// (an unknown tool name or unparseable tool arguments) a second time, after
// the one allowed re-prompt (06 §8). The runtime receives it and drives its
// retry/backoff/dead-letter path (04 §3).
var errMalformedRepeated = errors.New("brain: malformed model output repeated after re-prompt")

// errRoundTruncatedEmpty fails a pass when a round hit the output ceiling
// (maxOutputTokens) before producing anything the loop can carry forward: no
// assistant text, and no tool call complete enough to dispatch (06 §8). The
// only way to reach it is a single tool call whose arguments alone fill the
// whole budget, which no legitimate call does — so it goes to the runtime's
// retry/backoff/dead-letter path (04 §3) rather than being papered over with a
// synthesized turn. Dead-lettering is loud (notify.send + a system-error say),
// which is the point: a truncated round must never end a pass quietly.
var errRoundTruncatedEmpty = errors.New("brain: model round hit the output ceiling before producing usable output")

// truncationNotice is what a truncated round's re-prompt says (06 §5). It rides
// in the same user turn as the surviving calls' results, after them, and has
// three jobs: say the response was cut off, say plainly that the discarded
// final call did *not* run (so the model re-issues it rather than assuming it
// landed), and ask for a smaller round so the next one fits.
const truncationNotice = "Your previous response hit the output limit and was cut off partway through. " +
	"The tool results above cover only the calls that arrived complete. " +
	"The last call of that response was discarded and did NOT run — re-issue it if you still need it. " +
	"Keep this round smaller (fewer calls, shorter arguments) so it fits."

// orchestratorRole is the identity the system prompt is rendered with
// (01 §1): Kiln, the project orchestrator.
const orchestratorRole = "Kiln, the autonomous project orchestrator"

// MaxToolRounds bounds one pass's tool loop (06 §5, D4): after this many
// rounds without the model ending its turn, the brain appends a forced
// wrap-up instruction (at most a say) and stops. Typical passes are 1-3
// rounds; this is worst-case headroom, not a target. Raised from 8 to 12 with
// the CRUD consolidation (06 §4 amended): board state is now pulled via
// list_tickets / get_ticket rather than injected, so most passes spend a round
// or two reading before acting — the extra headroom absorbs those reads.
const MaxToolRounds = 12

// Service is the brain's core: HandleEvent (the runtime's Brain port,
// 04 §2) and the bounded tool loop it drives (06 §5). Constructed at the
// composition root over its ports (06 §9, 08 §7); stateless between calls.
type Service struct {
	board         BoardAPI
	reader        BoardReader
	say           Say
	notifications NotificationStore
	feed          FeedReader
	convo         ConversationReader
	agents        AgentInspector
	repo          RepoShell
	llm           LLM
	cfg           Config
}

// NewService assembles the brain over its ports and model configuration.
//
// The notifications write port and the feed read port (08 §7) are grouped after
// say. The full parameter order INTEGRATION wires is:
//
//	NewService(board BoardAPI, reader BoardReader, say Say,
//	    notifications NotificationStore, feed FeedReader,
//	    convo ConversationReader, agents AgentInspector, repo RepoShell,
//	    llm LLM, cfg Config)
//
// INTEGRATION passes rtSvc for notifications (*runtime.Service satisfies the
// port structurally, D5/§E4) and a feedReaderAdapter over rtSvc for feed.
func NewService(
	board BoardAPI, reader BoardReader, say Say, notifications NotificationStore, feed FeedReader,
	convo ConversationReader, agents AgentInspector, repo RepoShell, llm LLM, cfg Config,
) *Service {
	return &Service{
		board:         board,
		reader:        reader,
		say:           say,
		notifications: notifications,
		feed:          feed,
		convo:         convo,
		agents:        agents,
		repo:          repo,
		llm:           llm,
		cfg:           cfg,
	}
}

// HandleEvent is the runtime's Brain port (04 §2, 06 §9) — one call per
// event, invoked serially by the runtime's events worker (04 §4), so no two
// passes ever run concurrently. It builds this pass's context (types.go's
// PassInput, per 06 §3: full board snapshot + last TranscriptWindow
// transcript messages + the event, agent output truncated to
// AgentOutputTruncateBytes) and runs the bounded tool loop (runPass, 06 §5)
// to completion.
//
// Idempotency (06 §6) is not a mechanism here: a replayed call re-reads
// fresh state via reader/convo, so it sees whatever a crashed prior call
// already committed; the board's strict preconditions (03 D8) turn a
// re-issued action into ErrInvalidTransition, which the model receives as a
// tool result and is instructed (prompt.go) to treat as already done.
//
// Failure handling (06 §8): a returned error here means the pass failed —
// the runtime's events worker retries with backoff (04 §3), and after
// exhaustion dead-letters the event (notify.send + a system-error say, both
// the runtime's responsibility, not this method's). Board state is
// untouched by a dead event only insofar as this method itself never
// partially applies — each tool call within the loop is already committed by
// the time its result comes back (06 §8).
//
// This module's own Event type, not runtime.Event — see doc.go's
// no-runtime-import rule; the composition root adapts runtime.Event <-> Event
// when it wires *Service into the runtime's Brain port.
//
// Context assembly (06 §3 amended): the board is no longer read here — the
// model pulls it on demand via list_tickets / get_ticket (06 §4 amended), so a
// pass that does not need board state spends nothing on it. Only the last
// TranscriptWindow transcript messages are read once (convo, D2), alongside the
// triggering event; agent.turn_completed output is truncated to
// AgentOutputTruncateBytes at render time (renderContext).
func (s *Service) HandleEvent(ctx context.Context, ev Event) error {
	transcript, err := s.convo.Recent(ctx, TranscriptWindow)
	if err != nil {
		return fmt.Errorf("brain: read transcript: %w", err)
	}
	return s.runPass(ctx, PassInput{Transcript: transcript, Event: ev})
}

// model resolves the model id for this pass (06 §2): Config.Model, or
// DefaultModel when unset.
func (s *Service) model() string {
	if s.cfg.Model != "" {
		return s.cfg.Model
	}
	return DefaultModel
}

// runPass executes one bounded tool loop (06 §5) over an already-assembled
// PassInput:
//
//  1. Render the system prompt (prompt.go) and the input's three context
//     blocks into the first LLMRequest; call s.llm.Do.
//  2. For each returned ToolCall, run Dispatch (tool_dispatch.go) against the
//     ports and collect the ToolResults — including typed errors, verbatim.
//  3. Feed the results back as the next round's LLMMessage; repeat from (1)
//     until the model's StopReason is StopEndTurn or MaxToolRounds is hit,
//     at which point append a forced wrap-up instruction allowing at most a
//     say and make one final call.
//
// Truncated output (StopMaxTokens): a round that ran into maxOutputTokens is
// resumed, not returned from. Everything the model emitted before the cut is
// valid except its final tool call, whose JSON arguments may be sliced in half,
// so that one call is dropped, the rest are dispatched exactly as any other
// round's are, and truncationNotice rides back with their results telling the
// model the dropped call never ran. That keeps a truncated round's already-
// decided work while making the loop, not the model, responsible for noticing.
//
// Malformed output (06 §8): an unparseable tool call or unknown tool name
// yields StopMalformed; the first occurrence gets one re-prompt with the
// parse error appended, a second failure fails the pass. No mid-pass
// snapshot refresh, no streaming (06 §5) — the model sees the board exactly
// as of the moment HandleEvent started.
//
// See docs/specs/06-orchestrator-brain.md §5 and §8.
func (s *Service) runPass(ctx context.Context, input PassInput) error {
	system, err := RenderSystemPrompt(PromptData{
		Role:     orchestratorRole,
		DoneInPR: s.cfg.GateMode == GatePR,
	})
	if err != nil {
		return fmt.Errorf("brain: render system prompt: %w", err)
	}
	userText := renderContext(input)
	// The three context blocks (06 §3) go into one user message after the
	// fixed system prompt; the loop appends assistant tool calls and user
	// tool results as it goes.
	messages := []LLMMessage{{Role: LLMRoleUser, Text: userText}}

	// memo is this pass's memory of the calls it has already made (memo.go), so
	// a read the model asks for twice is answered from the conversation it is
	// already holding rather than the port, and a mutation the board refused is
	// not re-attempted while nothing has changed. Created here, discarded with
	// the pass: across passes the board does move.
	memo := newPassMemo()

	// reprompted tracks whether the previous round's malformed output has
	// already spent its single re-prompt (06 §8).
	reprompted := false

	for range MaxToolRounds {
		resp, err := s.llm.Do(ctx, LLMRequest{
			Model: s.model(), System: system, Messages: messages, Tools: Tools,
		})
		if err != nil {
			return fmt.Errorf("brain: llm call: %w", err)
		}
		if resp.StopReason == StopEndTurn {
			// A finished turn should carry nothing left to run. If it does —
			// the model ended mid-thought, or a stop reason this module maps
			// onto end_turn (refusal, pause_turn) arrived with tool_use blocks
			// attached — the pass is about to drop them, so it says so first.
			logUndispatchedCalls(ctx, "brain: pass ended with undispatched tool calls", resp.Calls)
			return nil
		}

		// A truncated round is resumed rather than returned from: its last call
		// is the only one that can be cut off, so it goes and the rest run.
		calls, truncated, err := dispatchableCalls(ctx, resp)
		if err != nil {
			return err
		}

		messages = append(messages, LLMMessage{
			Role: LLMRoleAssistant, Text: resp.Text, Calls: calls,
		})

		results, malformed := s.dispatchAll(ctx, memo, calls)
		if malformed {
			if reprompted {
				return errMalformedRepeated
			}
			reprompted = true
		} else {
			reprompted = false
		}

		messages = append(messages, resultsTurn(results, truncated))
	}

	// Round cap reached (06 §5, D4): one forced wrap-up round, at most a say.
	return s.forceWrapUp(ctx, memo, system, messages)
}

// dispatchableCalls narrows one round's tool calls to the ones the pass may
// actually run, and reports whether the round was truncated. An ordinary round
// is passed through whole; a truncated one loses its last call
// (dropTruncatedCall), and a truncated one that leaves nothing behind at all —
// no text, no complete call — fails the pass (errRoundTruncatedEmpty) rather
// than continuing on an empty turn.
func dispatchableCalls(ctx context.Context, resp LLMResponse) ([]ToolCall, bool, error) {
	if resp.StopReason != StopMaxTokens {
		return resp.Calls, false, nil
	}
	calls := dropTruncatedCall(ctx, resp)
	if resp.Text == "" && len(calls) == 0 {
		return nil, true, errRoundTruncatedEmpty
	}
	return calls, true, nil
}

// resultsTurn builds the user turn that carries a round's tool results back to
// the model, appending truncationNotice when the round was cut off — the one
// thing those results cannot say for themselves is that a call is missing from
// them because it was never run.
func resultsTurn(results []ToolResult, truncated bool) LLMMessage {
	msg := LLMMessage{Role: LLMRoleUser, Results: results}
	if truncated {
		msg.Text = truncationNotice
	}
	return msg
}

// dropTruncatedCall returns the calls of a truncated round that are safe to
// dispatch: every call except the last one.
//
// The last one goes unconditionally, without first asking whether it looks
// intact. A cut-off arguments object frequently still parses — truncate
// `{"id":"t-9","state":"done","done_commit":"abc123"}` before the commit and
// what is left is valid JSON describing a *different* action — so "does it
// unmarshal" is not a test of whether the model finished writing it. The only
// thing actually known is where the cut fell, and the fix for a wrongly dropped
// call is cheap: truncationNotice tells the model to re-issue it, and a re-read
// is answered from the pass memo (memo.go) rather than the port.
//
// Warn rather than Info: rounds this large are not the norm even at the raised
// maxOutputTokens ceiling, and a run of these records is the signal that the
// ceiling — or the prompt's round-size guidance — needs another look.
func dropTruncatedCall(ctx context.Context, resp LLMResponse) []ToolCall {
	kept := resp.Calls
	attrs := []any{"emitted", len(resp.Calls), "text_bytes", len(resp.Text)}
	if len(kept) > 0 {
		dropped := kept[len(kept)-1]
		kept = kept[:len(kept)-1]
		attrs = append(attrs, "dropped_tool", string(dropped.Name), "dropped_id", dropped.ID)
	}
	attrs = append(attrs, "dispatching", len(kept))
	slog.WarnContext(ctx, "brain: model round truncated at the output ceiling", attrs...)
	return kept
}

// logUndispatchedCalls surfaces tool calls a round emitted that the pass is not
// going to run. It exists so that "the model asked for something and nothing
// happened" is never inferable only from the absence of a record: every path
// that discards calls says which ones, by name and id, on the same turn_id as
// the rest of the pass. Error level, because a discarded call is the model's
// intent going nowhere — quiet in normal operation, findable when it isn't.
func logUndispatchedCalls(ctx context.Context, msg string, calls []ToolCall) {
	if len(calls) == 0 {
		return
	}
	slog.ErrorContext(ctx, msg, "count", len(calls), "tool_calls", summarizeCalls(calls))
}

// dispatchAll runs every tool call in a round against the ports, collecting
// the ToolResults and reporting whether any call was malformed (unknown tool
// name or unparseable arguments, 06 §8) as opposed to a plain tool error
// (a typed Board API error, fed back verbatim and not counted as malformed).
//
// The pass's memo (memo.go) threads through, so a call repeated within a round
// is caught by the same rule as one repeated a round later — the calls in a
// round run in order, so the first has already been recorded by the time the
// second is reached.
func (s *Service) dispatchAll(ctx context.Context, memo *passMemo, calls []ToolCall) ([]ToolResult, bool) {
	results := make([]ToolResult, 0, len(calls))
	malformed := false
	for _, call := range calls {
		res, m := s.dispatchOne(ctx, memo, call)
		if m {
			malformed = true
		}
		results = append(results, res)
	}
	return results, malformed
}

// forceWrapUp makes the single wrap-up call at the round cap (06 §5): the
// model is told to close out with at most a say, and only a say from that
// round is executed. The pass then ends regardless of what the model does.
//
// "Only a say is executed" means anything else the wrap-up round asks for is
// discarded — which is the intended behaviour, not an accident, so it is
// logged rather than left to be deduced from a missing effect
// (logUndispatchedCalls). A wrap-up round can be truncated like any other, so
// its last call is dropped on the same rule runPass uses.
func (s *Service) forceWrapUp(ctx context.Context, memo *passMemo, system string, messages []LLMMessage) error {
	messages = append(messages, LLMMessage{
		Role: LLMRoleUser,
		Text: "You have reached the tool-round limit for this turn. " +
			"Wrap up now with at most a single say to the user; do not call any other tool.",
	})
	resp, err := s.llm.Do(ctx, LLMRequest{
		Model: s.model(), System: system, Messages: messages, Tools: Tools,
	})
	if err != nil {
		return fmt.Errorf("brain: llm call (wrap-up): %w", err)
	}
	calls := resp.Calls
	if resp.StopReason == StopMaxTokens {
		calls = dropTruncatedCall(ctx, resp)
	}
	var skipped []ToolCall
	for _, call := range calls {
		if call.Name != ToolSay {
			skipped = append(skipped, call)
			continue
		}
		if res, _ := s.dispatchOne(ctx, memo, call); res.IsError {
			slog.ErrorContext(ctx, "brain: wrap-up say failed", "err", res.Content)
		}
	}
	logUndispatchedCalls(ctx, "brain: wrap-up round asked for tools other than say; not dispatched", skipped)
	return nil
}

// renderContext serializes one pass's two context blocks (06 §3 amended) into
// the single user message that follows the system prompt: the transcript
// window (§3.2) and the triggering event (§3.3, with agent output truncated).
// The board is not injected — the model reads it via list_tickets / get_ticket
// (06 §4 amended).
func renderContext(input PassInput) string {
	var b strings.Builder
	b.WriteString("# Conversation (last ")
	fmt.Fprintf(&b, "%d messages, oldest first)\n", TranscriptWindow)
	for _, m := range input.Transcript {
		fmt.Fprintf(&b, "[%s @ %s] %s\n", m.Role, m.At.Format("15:04:05"), m.Text)
	}
	b.WriteString("\n# Event\n")
	b.WriteString(renderEvent(input.Event))
	return b.String()
}

// doneRosterLimit caps how many Done tickets the roster lists. Done is the one
// column that only grows — every accepted ticket stays in it for the project's
// life — and the brain acts on its tail at most (what just landed), so listing
// all of it spends context on history no pass reads. The rest stays reachable
// on demand through search_tickets, which the elision line names.
const doneRosterLimit = 5

// renderBoard writes the snapshot's five columns in render order (06 §3.1).
// Written by hand rather than JSON-marshalled so the brain sees a compact,
// stable, model-friendly layout of every ticket — every one, except Done's
// older tail (doneRosterLimit).
func renderBoard(b *strings.Builder, snap board.Snapshot) {
	fmt.Fprintf(b, "workers: %d free / %d total\n", snap.WorkerFree, snap.WorkerTotal)
	renderColumn(b, "Shaping", board.StateShaping, snap.Shaping, 0)
	renderColumn(b, "Ready", board.StateReady, snap.Ready, 0)
	renderColumn(b, "Blocked", board.StateBlocked, snap.Blocked, 0)
	renderColumn(b, "Working", board.StateWorking, snap.Working, 0)
	// Snapshot.Done is newest-first (03 §4), so the head is the recent tail of
	// the project's history — the only part a pass is likely to act on.
	renderColumn(b, "Done", board.StateDone, snap.Done, doneRosterLimit)
}

// renderColumn writes one board column's tickets, one per line, under a header
// naming what that column's state accepts (allowedActions). The allowed set is
// a function of state alone, and a column *is* one state, so it is written once
// for the column rather than repeated on every row — the roster tells the model
// what it can do to these tickets at no per-ticket cost. An empty column has
// nothing to act on, so it stays a bare header.
//
// limit caps how many rows are listed (0 = all). The header always counts the
// whole column and an elided remainder is named explicitly, so a windowed column
// never reads as a complete one — the model can tell "no more" from "not shown".
func renderColumn(b *strings.Builder, label string, state board.State, tickets []board.Ticket, limit int) {
	fmt.Fprintf(b, "## %s (%d)\n", label, len(tickets))
	if len(tickets) > 0 {
		fmt.Fprintf(b, "%s on these: %s\n", allowedPrefix, allowedActions(state))
	}
	shown := tickets
	if limit > 0 && len(shown) > limit {
		shown = shown[:limit]
	}
	for _, t := range shown {
		fmt.Fprintf(b, "- [%s] %q (state=%s, priority=%d)", t.ID, t.Title, t.State, t.Priority)
		if t.BlockedReason != nil {
			fmt.Fprintf(b, " blocked_reason=%q", *t.BlockedReason)
		}
		// A queued ticket held by its dependencies looks identical to a stalled
		// one in a bare roster, so the roster says which it is (0013).
		if t.WaitingOnDependencies() {
			fmt.Fprintf(b, " waiting_on=%d unfinished dependencies", t.UnmetDependencies)
		}
		b.WriteByte('\n')
	}
	if len(shown) < len(tickets) {
		fmt.Fprintf(b, "(%d older not shown — find them with %s)\n", len(tickets)-len(shown), ToolSearchTickets)
	}
}

// renderEvent decodes and formats the triggering event (06 §3.3). Long
// agent.turn_completed output is truncated to AgentOutputTruncateBytes,
// head+tail — the brain judges outcomes, it does not re-review diffs.
func renderEvent(ev Event) string {
	switch ev.Type {
	case EventHumanMessage:
		var p HumanMessagePayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return fmt.Sprintf("human.message (unparseable payload: %v)", err)
		}
		return "The user said: " + p.Text
	case EventAgentTurnCompleted:
		var p AgentTurnCompletedPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return fmt.Sprintf("agent.turn_completed (unparseable payload: %v)", err)
		}
		out := truncateHeadTail(p.Output, AgentOutputTruncateBytes)
		return fmt.Sprintf(
			"agent.turn_completed on ticket %s (worker %s, is_error=%t, cost $%.4f):\n%s",
			p.TicketID, p.WorkerID, p.IsError, p.CostUSD, out,
		)
	default:
		return fmt.Sprintf("event %s", ev.Type)
	}
}

// truncateHeadTail keeps the first and last budget/2 bytes of s with an
// elision marker between them when s exceeds budget (06 §3.3). Byte-based:
// agent output is treated as opaque text, not re-parsed.
func truncateHeadTail(s string, budget int) string {
	if len(s) <= budget {
		return s
	}
	// Split the budget between a head slice and a tail slice.
	const halves = 2
	half := budget / halves
	return s[:half] + "\n…[output truncated]…\n" + s[len(s)-half:]
}
