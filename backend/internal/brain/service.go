package brain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/crabtree-michael/kiln/backend/internal/board"
)

// errMalformedRepeated fails a pass when the model emits malformed output
// (an unknown tool name or unparseable tool arguments) a second time, after
// the one allowed re-prompt (06 §8). The runtime receives it and drives its
// retry/backoff/dead-letter path (04 §3).
var errMalformedRepeated = errors.New("brain: malformed model output repeated after re-prompt")

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
//  2. For each returned ToolCall, run Dispatch (tools.go) against the
//     ports and collect the ToolResults — including typed errors, verbatim.
//  3. Feed the results back as the next round's LLMMessage; repeat from (1)
//     until the model's StopReason is StopEndTurn or MaxToolRounds is hit,
//     at which point append a forced wrap-up instruction allowing at most a
//     say and make one final call.
//
// Malformed output (06 §8): an unparseable tool call or unknown tool name
// yields StopMalformed; the first occurrence gets one re-prompt with the
// parse error appended, a second failure fails the pass. No mid-pass
// snapshot refresh, no streaming (06 §5) — the model sees the board exactly
// as of the moment HandleEvent started.
//
// See docs/specs/06-orchestrator-brain.md §5 and §8.
func (s *Service) runPass(ctx context.Context, input PassInput) error {
	system, err := RenderSystemPrompt(PromptData{Role: orchestratorRole})
	if err != nil {
		return fmt.Errorf("brain: render system prompt: %w", err)
	}
	userText := renderContext(input)
	// The three context blocks (06 §3) go into one user message after the
	// fixed system prompt; the loop appends assistant tool calls and user
	// tool results as it goes.
	messages := []LLMMessage{{Role: LLMRoleUser, Text: userText}}

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
			return nil
		}

		messages = append(messages, LLMMessage{
			Role: LLMRoleAssistant, Text: resp.Text, Calls: resp.Calls,
		})

		results, malformed := s.dispatchAll(ctx, resp.Calls)
		if malformed {
			if reprompted {
				return errMalformedRepeated
			}
			reprompted = true
		} else {
			reprompted = false
		}

		messages = append(messages, LLMMessage{Role: LLMRoleUser, Results: results})
	}

	// Round cap reached (06 §5, D4): one forced wrap-up round, at most a say.
	return s.forceWrapUp(ctx, system, messages)
}

// dispatchAll runs every tool call in a round against the ports, collecting
// the ToolResults and reporting whether any call was malformed (unknown tool
// name or unparseable arguments, 06 §8) as opposed to a plain tool error
// (a typed Board API error, fed back verbatim and not counted as malformed).
func (s *Service) dispatchAll(ctx context.Context, calls []ToolCall) ([]ToolResult, bool) {
	results := make([]ToolResult, 0, len(calls))
	malformed := false
	for _, call := range calls {
		res, m := s.dispatchOne(ctx, call)
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
func (s *Service) forceWrapUp(ctx context.Context, system string, messages []LLMMessage) error {
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
	for _, call := range resp.Calls {
		if call.Name == ToolSay {
			s.dispatchOne(ctx, call)
		}
	}
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

// renderBoard writes the snapshot's five columns in render order (06 §3.1).
// Written by hand rather than JSON-marshalled so the brain sees a compact,
// stable, model-friendly layout of every ticket.
func renderBoard(b *strings.Builder, snap board.Snapshot) {
	fmt.Fprintf(b, "workers: %d free / %d total\n", snap.WorkerFree, snap.WorkerTotal)
	renderColumn(b, "Shaping", snap.Shaping)
	renderColumn(b, "Ready", snap.Ready)
	renderColumn(b, "Blocked", snap.Blocked)
	renderColumn(b, "Working", snap.Working)
	renderColumn(b, "Done", snap.Done)
}

// renderColumn writes one board column's tickets, one per line.
func renderColumn(b *strings.Builder, label string, tickets []board.Ticket) {
	fmt.Fprintf(b, "## %s (%d)\n", label, len(tickets))
	for _, t := range tickets {
		fmt.Fprintf(b, "- [%s] %q (state=%s, priority=%d)", t.ID, t.Title, t.State, t.Priority)
		if t.BlockedReason != nil {
			fmt.Fprintf(b, " blocked_reason=%q", *t.BlockedReason)
		}
		b.WriteByte('\n')
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
