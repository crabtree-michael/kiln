package brain

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// DefaultModel is the default Anthropic model id (06 §2, D1): a
// tool-following dispatcher over a small board favors strong tool-use at low
// latency and cost over Opus's better judgment or Haiku's lower cost.
const DefaultModel = "claude-sonnet-5"

// ModelEnvVar overrides DefaultModel when set (06 §2, D1). Normally parsed
// into Config.Model at the composition root; the Adapter also consults it
// directly as a fallback so it is usable standalone.
const ModelEnvVar = "KILN_BRAIN_MODEL"

// maxOutputTokens caps one round's generation. The brain emits short tool
// calls and status text, not long prose, so a small ceiling is plenty and
// keeps latency down (06 §5's cost/latency envelope).
const maxOutputTokens = 4096

// Config is the brain's model configuration (06 §2), read at the
// composition root (04 §8) from KILN_BRAIN_MODEL (default DefaultModel).
// This module only declares the default and the shape; env parsing happens
// in backend/cmd/kiln.
type Config struct {
	Model string
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

	// StopMalformed is synthesized by this module, never returned by the LLM
	// port itself: an unparseable tool call or unknown tool name (06 §8).
	// Triggers the one-re-prompt-then-fail rule.
	StopMalformed StopReason = "malformed"
)

// LLMRequest is one round-trip to the model (06 §2, §5): the fixed system
// prompt (prompt.go), the conversation so far, and the fixed tool schema
// (tools.go). No streaming (06 §5, D4) and no sampling overrides — SDK
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
//   - The system block carries a breakpoint. tools render before system, so
//     this caches the whole fixed prefix (the 14-tool schema + the static
//     system prompt) as one unit. That prefix is byte-identical every pass —
//     the prompt template interpolates only the constant Role, and Tools is a
//     fixed slice — so back-to-back passes read it instead of re-billing it.
//   - The last content block of the conversation carries a breakpoint
//     (markConversationBreakpoint). Within one pass the bounded tool loop
//     re-sends a growing conversation up to MaxToolRounds times; each round's
//     breakpoint lets the next round read everything through the prior round.
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
	}
	if req.System != "" {
		params.System = []anthropic.TextBlockParam{{
			Text:         req.System,
			CacheControl: anthropic.NewCacheControlEphemeralParam(),
		}}
	}

	msg, err := a.client.Messages.New(ctx, params)
	if err != nil {
		return LLMResponse{}, fmt.Errorf("brain: anthropic messages.new: %w", err)
	}
	a.logUsage(ctx, msg.Usage)
	return fromSDKMessage(msg), nil
}

// logUsage emits one round's token usage so the live eval set can confirm
// cache hit rates. cache_read_input_tokens staying at zero across a pass's
// later rounds means a silent invalidator crept into the prefix (a volatile
// system prompt, non-deterministic tool ordering). input_tokens is the
// uncached remainder only — the true prompt size is the sum of all three.
func (a *Adapter) logUsage(ctx context.Context, u anthropic.Usage) {
	a.log().LogAttrs(ctx, slog.LevelDebug, "brain: llm round usage",
		slog.Int64("input_tokens", u.InputTokens),
		slog.Int64("output_tokens", u.OutputTokens),
		slog.Int64("cache_read_input_tokens", u.CacheReadInputTokens),
		slog.Int64("cache_creation_input_tokens", u.CacheCreationInputTokens),
	)
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
		// LLMRoleUser: the context block (round 1) and/or tool results.
		blocks := make([]anthropic.ContentBlockParamUnion, 0, len(m.Results)+1)
		if m.Text != "" {
			blocks = append(blocks, anthropic.NewTextBlock(m.Text))
		}
		for _, r := range m.Results {
			blocks = append(blocks, anthropic.NewToolResultBlock(r.ToolCallID, r.Content, r.IsError))
		}
		out = append(out, anthropic.NewUserMessage(blocks...))
	}
	return out
}

// markConversationBreakpoint sets a cache-control breakpoint on the last
// content block of the last message, so within a pass each round reads the
// conversation prefix the previous round wrote (see Do). The last message is
// always a user turn — the round-1 context block (OfText), later rounds' tool
// results (OfToolResult), or the forced wrap-up text (OfText) — so those are
// the only variants handled; anything else is left unmarked rather than
// guessed at. A round appends at most a handful of blocks, well inside the
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

// toSDKTools maps the fixed tool set (tools.go) onto the SDK's tool params.
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
	// Only tool_use continues the loop; every other stop reason ends the pass.
	if msg.StopReason == anthropic.StopReasonToolUse {
		resp.StopReason = StopToolUse
	} else {
		resp.StopReason = StopEndTurn
	}
	return resp
}
