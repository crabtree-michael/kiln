package brain

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/crabtree-michael/kiln/backend/internal/obs"
)

// DefaultModel is the default Anthropic model id (06 §2, D1): Sonnet 5 gives a
// tool-following dispatcher over a small board stronger judgment than Haiku
// while thinking stays disabled (see Do) to hold the dispatcher's low latency
// and modest maxOutputTokens ceiling. Override via KILN_BRAIN_MODEL.
const DefaultModel = "claude-sonnet-5"

// ModelEnvVar overrides DefaultModel when set (06 §2, D1). Normally parsed
// into Config.Model at the composition root; the Adapter also consults it
// directly as a fallback so it is usable standalone.
const ModelEnvVar = "KILN_BRAIN_MODEL"

// Effort is one round's output-effort level — Anthropic's
// output_config.effort, which governs how much deliberation and how many
// tokens the model spends before answering. Declared here rather than taken
// from the SDK so Config stays free of provider types, like the rest of this
// module's ports.
type Effort string

const (
	EffortLow    Effort = "low"
	EffortMedium Effort = "medium"
	EffortHigh   Effort = "high"
	EffortXHigh  Effort = "xhigh"
	EffortMax    Effort = "max"
)

// DefaultEffort is the default output effort (06 §2). The API's own default is
// "high", which is the wrong shape for this caller: the brain is a tool-calling
// dispatcher with thinking explicitly disabled (see Do), so it spends the extra
// deliberation budget on every round of every pass without a decision that
// needs it. Medium buys that cost and latency back. Override via
// KILN_BRAIN_EFFORT.
const DefaultEffort = EffortMedium

// EffortEnvVar overrides DefaultEffort when set (06 §2). Same treatment as
// ModelEnvVar: normally parsed into Config.Effort at the composition root, with
// the Adapter also consulting it directly so it is usable standalone.
const EffortEnvVar = "KILN_BRAIN_EFFORT"

// maxOutputTokens caps one round's generation. The brain emits short tool
// calls and status text, not long prose, so the ceiling stays modest to keep
// latency down (06 §5's cost/latency envelope) — but not as modest as it was.
//
// Raised from 4096 after a large round was truncated against it. Typical rounds
// are nowhere near either number; the ones that are near it are the ones that
// matter most — a batched read round (prompt.go's ## Rounds) followed by
// several send_to_agent instructions is easily a few thousand tokens of tool
// arguments, and at 4096 that round came back cut off mid-call. This is a
// non-streaming request (06 §5, D4), so the ceiling also has to stay inside the
// SDK's default HTTP timeout; 16384 is comfortably under it while making
// truncation rare.
//
// Rare, not impossible — the loop still handles it rather than assuming it
// away. See StopMaxTokens and runPass.
const maxOutputTokens = 16384

// roundTextSummaryBytes bounds how much of one round's assistant text a log
// record carries. Same budget as the agent module's output summary: enough to
// read what the model actually said without a runaway record when a round
// generates to the maxOutputTokens ceiling.
const roundTextSummaryBytes = 1024

// GateMode names which condition satisfies a ticket's merge gate (06 §7). The
// zero value ("") is treated as GateMain, so a project that never set the knob
// keeps the original merged-to-main behavior.
type GateMode string

const (
	// GateMain accepts a done only once its commit is on origin/main.
	GateMain GateMode = "main"
	// GatePR accepts a done once the work exists in a pull request.
	GatePR GateMode = "pr"
)

// Config is the brain's per-project configuration (06 §2), resolved at the
// composition root (04 §8) from the project's stored settings. This module only
// declares the defaults and the shape; the wiring lives in backend/cmd/kiln.
type Config struct {
	Model string
	// Effort is the per-round output-effort level (06 §2); empty means
	// DefaultEffort. Backend-only, exactly like Model — not a per-project knob.
	Effort Effort
	// GateMode selects the done gate (06 §7); empty means GateMain.
	GateMode GateMode
}

// ToolCall is one tool_use block the model returned in a round (06 §5).
type ToolCall struct {
	ID    string
	Name  ToolName
	Input json.RawMessage
}

// ToolResult is one tool_result block fed back to the model (06 §5, §8).
// Content is the port call's return value or a typed error's Error() text,
// verbatim — both the tool-error-recovery loop (§8) and the idempotency rule
// (§6, "treat ErrInvalidTransition as already done") depend on the model
// seeing this literally, not summarized or wrapped.
type ToolResult struct {
	ToolCallID string
	Content    string
	IsError    bool
}

// LLMRole distinguishes the two message roles in the Anthropic conversation
// this module drives (06 §5).
type LLMRole string

const (
	LLMRoleUser      LLMRole = "user"      // the context block (round 1) or tool results (later rounds)
	LLMRoleAssistant LLMRole = "assistant" // a previous round's text + tool calls
)

// LLMMessage is one turn of the pass's Anthropic conversation (06 §5). Kept
// minimal and provider-shaped on purpose: the composition root's Anthropic
// adapter maps this directly onto SDK message params (see Adapter's wire-in
// note below) without this module importing the SDK's types.
type LLMMessage struct {
	Role    LLMRole
	Text    string       // user: the context block on round 1; assistant: any accompanying text
	Calls   []ToolCall   // assistant turn: tool_use blocks returned by a previous round
	Results []ToolResult // user turn: tool_result blocks, one per the previous Calls
}

// StopReason is why a round ended (06 §5, §8).
type StopReason string

const (
	StopToolUse StopReason = "tool_use" // the model wants to call one or more tools; the loop continues
	StopEndTurn StopReason = "end_turn" // the model is done; the pass ends

	// StopMaxTokens is a round the model did not finish: it ran into
	// maxOutputTokens mid-generation, so its last content block — quite
	// possibly a tool call's JSON arguments — is cut off partway through.
	//
	// It is its own stop reason rather than another way of spelling
	// StopEndTurn because the two mean opposite things ("done" versus "cut
	// off") and the loop has to tell them apart. Folded together, a truncated
	// round returned from the pass exactly as a finished one does, taking
	// whatever calls the model had already emitted with it and saying nothing
	// — the round vanished. runPass now dispatches what survived the cut and
	// re-prompts for the rest.
	StopMaxTokens StopReason = "max_tokens"

	// StopMalformed is synthesized by this module, never returned by the LLM
	// port itself: an unparseable tool call or unknown tool name (06 §8).
	// Triggers the one-re-prompt-then-fail rule.
	StopMalformed StopReason = "malformed"
)

// LLMRequest is one round-trip to the model (06 §2, §5): the fixed system
// prompt (prompt.go), the conversation so far, and the fixed tool schema
// (tool_schemas.go). No streaming (06 §5, D4) and no sampling overrides — SDK
// defaults until the golden tests say otherwise (06 §2).
type LLMRequest struct {
	Model    string
	System   string
	Messages []LLMMessage
	Tools    []ToolDef
}

// LLMResponse is the model's answer for one round (06 §5).
type LLMResponse struct {
	StopReason StopReason
	Text       string     // accompanying or final text
	Calls      []ToolCall // present when StopReason is StopToolUse
}

// LLM is the brain's port onto the model call (06 §2, §5, §9): one round of
// the bounded tool loop (service.go). The composition root wires this to the
// Anthropic Go SDK via Adapter, below; the primary test suite (golden
// decision tests, 06 §9) uses a scripted fake that plays back a fixed
// LLMResponse sequence — no real network call in the commit gate.
type LLM interface {
	Do(ctx context.Context, req LLMRequest) (LLMResponse, error)
}

// Adapter is the Anthropic Go SDK client behind LLM (06 §2, §9). It
// translates LLMRequest/LLMResponse to/from the SDK's Messages.New call:
// System, Messages, and Tools map onto the SDK's params types; the SDK's
// content-block union (text vs tool_use) maps onto LLMResponse.Text/Calls;
// the SDK's stop_reason maps onto StopReason. The golden tests (06 §9) use a
// scripted fake instead, so this path is exercised by the live eval set, not
// the commit gate.
type Adapter struct {
	Config Config
	client anthropic.Client
	logger *slog.Logger // nil → slog.Default(); see log()
}

var _ LLM = (*Adapter)(nil)

// NewAdapter constructs the Anthropic adapter. The SDK reads ANTHROPIC_API_KEY
// (and the other standard credential sources) from the environment.
func NewAdapter(cfg Config) *Adapter {
	return &Adapter{Config: cfg, client: anthropic.NewClient()}
}

// NewAdapterWithClient injects a preconfigured SDK client (e.g. a custom
// base URL or API key via option.With...), for the composition root and
// live-eval harness.
func NewAdapterWithClient(cfg Config, opts ...option.RequestOption) *Adapter {
	return &Adapter{Config: cfg, client: anthropic.NewClient(opts...)}
}

// Do runs one round of the pass's conversation against the Anthropic API.
//
// Two prompt-caching breakpoints are placed (06 §5 cost/latency envelope).
// Prompt caching is a prefix match over the rendered request (tools → system →
// messages), so the placement follows the two reuse boundaries the brain has:
//
//   - The system block carries a breakpoint with a 1-hour TTL. tools render
//     before system, so this caches the whole fixed prefix (the 14-tool schema
//   - the static system prompt) as one unit. That prefix is byte-identical
//     every pass — the prompt template interpolates only the constant Role,
//     and Tools is a fixed slice — so every pass within the TTL reads it
//     instead of re-billing it. The 1h TTL (2x write vs 1.25x for the default
//     5m) is deliberate: passes are event-driven and an agent turn routinely
//     takes tens of minutes, so consecutive passes usually fall outside a 5m
//     window and the default TTL made nearly every pass a cold write.
//   - The last content block of the conversation carries a breakpoint
//     (markConversationBreakpoint). Within one pass the bounded tool loop
//     re-sends a growing conversation up to MaxToolRounds times; each round's
//     breakpoint lets the next round read everything through the prior round.
//     Rounds are seconds apart, so this one keeps the default 5m TTL and its
//     cheaper writes.
//
// Two breakpoints is well under the 4-per-request ceiling. Caching is
// transparent to the scripted-fake golden suite (06 §9) — only this live
// Adapter builds MessageNewParams, so the commit gate is unaffected.
func (a *Adapter) Do(ctx context.Context, req LLMRequest) (LLMResponse, error) {
	messages := toSDKMessages(req.Messages)
	markConversationBreakpoint(messages)

	params := anthropic.MessageNewParams{
		Model:     a.model(),
		MaxTokens: maxOutputTokens,
		Messages:  messages,
		Tools:     toSDKTools(req.Tools),
		// Thinking stays off. Sonnet 5 (and 4.6+) run adaptive thinking whenever
		// the field is omitted, and MaxTokens caps thinking + tool calls + text
		// together — so an omitted field could spend the maxOutputTokens
		// budget thinking and truncate a round's tool calls. Disabling keeps the
		// brain a lean, low-latency dispatcher; a no-op on models that don't think
		// by default (e.g. Haiku). Revisit if the eval set (§9) wants deliberation.
		Thinking: anthropic.ThinkingConfigParamUnion{OfDisabled: &anthropic.ThinkingConfigDisabledParam{}},
		// Effort is set explicitly rather than left to the API's "high" default
		// (06 §2): with thinking off, a dispatcher round is a tool selection, not
		// a deliberation, so the default's budget was being paid on every round of
		// every pass. See DefaultEffort.
		OutputConfig: anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffort(a.effort())},
	}
	if req.System != "" {
		params.System = []anthropic.TextBlockParam{{
			Text:         req.System,
			CacheControl: anthropic.CacheControlEphemeralParam{TTL: anthropic.CacheControlEphemeralTTLTTL1h},
		}}
	}

	msg, err := a.client.Messages.New(ctx, params)
	if err != nil {
		return LLMResponse{}, fmt.Errorf("brain: anthropic messages.new: %w", err)
	}
	resp := fromSDKMessage(msg)
	a.logRound(ctx, msg.Usage, resp)
	return resp, nil
}

// logRound emits one record per LLM round carrying both what the round cost and
// what the model actually said, keyed (via the context handler) to the pass's
// turn id so a whole pass reads back in order.
//
// The usage half makes cache hit rates observable in production (the
// slog→Sentry-Logs bridge ships Info+ records with typed attributes) as well as
// by the live eval set. cache_read_input_tokens staying at zero across a pass's
// later rounds means a silent invalidator crept into the prefix (a volatile
// system prompt, non-deterministic tool ordering). input_tokens is the uncached
// remainder only — the true prompt size is the sum of all three.
//
// The output half is the model's own message: the assistant text, the tool
// calls it asked for, and the stop reason that decides whether the bounded loop
// continues. Without it the only trace of a pass was brain.tool's per-call
// records, which show the calls but never the reasoning text the model wrapped
// them in — and nothing at all for a round that ends the pass with text only.
// Text is summarized (obs.Summary) rather than hashed: unlike a redelivered
// instruction, what matters here is reading it, not matching it.
//
// Info rather than Debug on purpose: one compact record per LLM round is cheap,
// and it is the only signal that distinguishes a cold cache write from a read
// after deploy.
//
// The two cache-write counts break the aggregate down by the TTL each write
// bought, because the two bill at different multiples of the base input rate
// (5m at 1.25×, 1h at 2×). Cache writes are 40–60% of the brain's spend, so
// without the split a round's cost is only knowable as a range — which is what
// made the 2026-08-05 optimization pass quote $192–$374/30d instead of a
// number. They sum to cache_creation_input_tokens; logging all three keeps the
// aggregate comparable across the records written before the split existed.
func (a *Adapter) logRound(ctx context.Context, u anthropic.Usage, resp LLMResponse) {
	a.log().LogAttrs(ctx, slog.LevelInfo, "brain: llm round",
		slog.Int64("input_tokens", u.InputTokens),
		slog.Int64("output_tokens", u.OutputTokens),
		slog.Int64("cache_read_input_tokens", u.CacheReadInputTokens),
		slog.Int64("cache_creation_input_tokens", u.CacheCreationInputTokens),
		slog.Int64("cache_creation_5m_input_tokens", u.CacheCreation.Ephemeral5mInputTokens),
		slog.Int64("cache_creation_1h_input_tokens", u.CacheCreation.Ephemeral1hInputTokens),
		slog.String("stop_reason", string(resp.StopReason)),
		slog.String("text", obs.Summary(resp.Text, roundTextSummaryBytes)),
		slog.String("tool_calls", summarizeCalls(resp.Calls)),
	)
}

// summarizeCalls renders a round's tool_use blocks as a compact "name#id" list
// — empty when the round called nothing. Names and ids only: dispatchOne
// (tool_dispatch.go) already logs each call's arguments and result on its own record,
// and the id is what joins the two under the same turn_id.
func summarizeCalls(calls []ToolCall) string {
	parts := make([]string, 0, len(calls))
	for _, c := range calls {
		parts = append(parts, string(c.Name)+"#"+c.ID)
	}
	return strings.Join(parts, " ")
}

// log returns the adapter's logger, defaulting to slog.Default() so the
// composition-root and live-eval constructors need not set one.
func (a *Adapter) log() *slog.Logger {
	if a.logger != nil {
		return a.logger
	}
	return slog.Default()
}

// model resolves the model id (06 §2): Config.Model, else the KILN_BRAIN_MODEL
// env var, else DefaultModel.
func (a *Adapter) model() string {
	if a.Config.Model != "" {
		return a.Config.Model
	}
	if env := os.Getenv(ModelEnvVar); env != "" {
		return env
	}
	return DefaultModel
}

// effort resolves the output-effort level (06 §2): Config.Effort, else the
// KILN_BRAIN_EFFORT env var, else DefaultEffort. An unrecognized value is
// passed through rather than corrected — the API rejects it loudly, which is
// easier to diagnose than a silent downgrade of an intended setting.
func (a *Adapter) effort() Effort {
	if a.Config.Effort != "" {
		return a.Config.Effort
	}
	if env := os.Getenv(EffortEnvVar); env != "" {
		return Effort(env)
	}
	return DefaultEffort
}

// toSDKMessages maps this module's conversation onto the SDK's message params.
func toSDKMessages(msgs []LLMMessage) []anthropic.MessageParam {
	out := make([]anthropic.MessageParam, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == LLMRoleAssistant {
			blocks := make([]anthropic.ContentBlockParamUnion, 0, len(m.Calls)+1)
			if m.Text != "" {
				blocks = append(blocks, anthropic.NewTextBlock(m.Text))
			}
			for _, c := range m.Calls {
				blocks = append(blocks, anthropic.ContentBlockParamUnion{
					OfToolUse: &anthropic.ToolUseBlockParam{
						ID:    c.ID,
						Name:  string(c.Name),
						Input: c.Input,
					},
				})
			}
			out = append(out, anthropic.NewAssistantMessage(blocks...))
			continue
		}
		// LLMRoleUser: the context block (round 1) and/or tool results, and
		// possibly both — a truncated round comes back as its surviving calls'
		// results plus a note explaining the cut (runPass). Results are written
		// first: the API wants a turn's tool_result blocks at the head of its
		// content, and a round with no results is unaffected by the ordering.
		blocks := make([]anthropic.ContentBlockParamUnion, 0, len(m.Results)+1)
		for _, r := range m.Results {
			blocks = append(blocks, anthropic.NewToolResultBlock(r.ToolCallID, r.Content, r.IsError))
		}
		if m.Text != "" {
			blocks = append(blocks, anthropic.NewTextBlock(m.Text))
		}
		out = append(out, anthropic.NewUserMessage(blocks...))
	}
	return out
}

// markConversationBreakpoint sets a cache-control breakpoint on the last
// content block of the last message, so within a pass each round reads the
// conversation prefix the previous round wrote (see Do). The last message is
// always a user turn — the round-1 context block (OfText), later rounds' tool
// results (OfToolResult), a truncated round's results-then-note (OfText), or
// the forced wrap-up text (OfText) — so those are the only variants handled;
// anything else is left unmarked rather than guessed at. A round appends at
// most a handful of blocks, well inside the
// 20-block cache lookback window, so the incremental reads never miss.
func markConversationBreakpoint(msgs []anthropic.MessageParam) {
	if len(msgs) == 0 {
		return
	}
	blocks := msgs[len(msgs)-1].Content
	if len(blocks) == 0 {
		return
	}
	last := &blocks[len(blocks)-1]
	switch {
	case last.OfText != nil:
		last.OfText.CacheControl = anthropic.NewCacheControlEphemeralParam()
	case last.OfToolResult != nil:
		last.OfToolResult.CacheControl = anthropic.NewCacheControlEphemeralParam()
	}
}

// toSDKTools maps the fixed tool set (tool_schemas.go) onto the SDK's tool params.
func toSDKTools(defs []ToolDef) []anthropic.ToolUnionParam {
	out := make([]anthropic.ToolUnionParam, 0, len(defs))
	for _, d := range defs {
		schema := anthropic.ToolInputSchemaParam{Properties: d.InputSchema[schemaKeyProperties]}
		if req, ok := d.InputSchema[schemaKeyRequired].([]string); ok {
			schema.Required = req
		}
		out = append(out, anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
			Name:        string(d.Name),
			Description: anthropic.String(d.Description),
			InputSchema: schema,
		}})
	}
	return out
}

// fromSDKMessage maps one SDK response onto LLMResponse.
func fromSDKMessage(msg *anthropic.Message) LLMResponse {
	resp := LLMResponse{}
	for _, block := range msg.Content {
		switch v := block.AsAny().(type) {
		case anthropic.TextBlock:
			resp.Text += v.Text
		case anthropic.ToolUseBlock:
			resp.Calls = append(resp.Calls, ToolCall{
				ID:    v.ID,
				Name:  ToolName(v.Name),
				Input: json.RawMessage(v.JSON.Input.Raw()),
			})
		}
	}
	// tool_use continues the loop and max_tokens resumes it (runPass); every
	// other stop reason ends the pass. max_tokens is called out by name rather
	// than swept into the default because "the model stopped" and "the model
	// was stopped" need opposite handling — see StopMaxTokens.
	switch msg.StopReason {
	case anthropic.StopReasonToolUse:
		resp.StopReason = StopToolUse
	case anthropic.StopReasonMaxTokens:
		resp.StopReason = StopMaxTokens
	case anthropic.StopReasonEndTurn, anthropic.StopReasonStopSequence,
		anthropic.StopReasonPauseTurn, anthropic.StopReasonRefusal:
		resp.StopReason = StopEndTurn
	default:
		// A stop reason added to the SDK since this switch was written. Ending
		// the pass is the safe reading of an unknown one, and the loop says so
		// out loud if the round was carrying tool calls (logUndispatchedCalls).
		resp.StopReason = StopEndTurn
	}
	return resp
}
