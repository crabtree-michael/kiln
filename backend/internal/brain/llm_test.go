package brain_test

// Unit tests for the Anthropic SDK adapter (llm.go, 06 §2/§9). The golden
// decision tests drive the brain through the scripted LLM fake, so the real
// Adapter — the LLMRequest→SDK→LLMResponse translation — is otherwise only
// exercised by the live eval set. These tests pin that translation offline by
// pointing a real Adapter at an httptest server standing in for the Anthropic
// API: they assert what request the SDK put on the wire (model, system,
// messages of both roles with text/tool_use/tool_result blocks, the tool
// schema) and how a canned response Message maps back onto LLMResponse.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/crabtree-michael/kiln/backend/internal/brain"
)

const (
	testAPIKey    = "test-key"
	pathMessages  = "/v1/messages"
	modelOverride = "claude-test-model"
	callID1       = "call-1"
	keyType       = "type"
	keyText       = "text"
)

// The usage attribute names, shared by the response bodies the stub returns and
// the round records the assertions read back — the two must agree, and goconst
// objects to spelling either out twice.
const (
	keyInputTokens     = "input_tokens"
	keyOutputTokens    = "output_tokens"
	keyCacheRead       = "cache_read_input_tokens"
	keyCacheCreation   = "cache_creation_input_tokens"
	keyCacheCreation5m = "cache_creation_5m_input_tokens"
	keyCacheCreation1h = "cache_creation_1h_input_tokens"
)

// asMap and asSlice narrow a decoded-JSON any to the expected shape, failing
// the test loudly (rather than discarding the assertion's ok) so errcheck's
// type-assertion rule stays satisfied.
func asMap(t *testing.T, label string, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("%s: %v is %T, want object", label, v, v)
	}
	return m
}

func asSlice(t *testing.T, label string, v any) []any {
	t.Helper()
	s, ok := v.([]any)
	if !ok {
		t.Fatalf("%s: %v is %T, want array", label, v, v)
	}
	return s
}

// anthropicStub spins an httptest server standing in for POST /v1/messages. It
// captures the decoded request body (for wire assertions) and replies with the
// supplied response Message JSON. An unrouted request fails the test loudly.
type anthropicStub struct {
	lastBody map[string]any
	raw      []byte
}

func newAdapterAgainst(t *testing.T, cfg brain.Config, respStatus int, respBody any) (*brain.Adapter, *anthropicStub) {
	t.Helper()
	stub := &anthropicStub{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != pathMessages {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		stub.raw = body
		if err := json.Unmarshal(body, &stub.lastBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(respStatus)
		if err := json.NewEncoder(w).Encode(respBody); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	adapter := brain.NewAdapterWithClient(cfg,
		option.WithBaseURL(srv.URL),
		option.WithAPIKey(testAPIKey),
		option.WithHTTPClient(srv.Client()),
		option.WithMaxRetries(0),
	)
	return adapter, stub
}

// message builds a minimal but valid Anthropic response Message with the given
// stop reason and content blocks.
func message(stopReason string, content ...map[string]any) map[string]any {
	return map[string]any{
		"id":            "msg_1",
		keyType:         "message",
		"role":          "assistant",
		"model":         modelOverride,
		"content":       content,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage":         map[string]any{keyInputTokens: 1, keyOutputTokens: 1},
	}
}

// messageWithUsage is message() with the response's usage block replaced, so a
// test can pin what logRound makes of the token counts the API reports.
func messageWithUsage(stopReason string, usage map[string]any, content ...map[string]any) map[string]any {
	msg := message(stopReason, content...)
	msg["usage"] = usage
	return msg
}

func textBlock(text string) map[string]any {
	return map[string]any{keyType: keyText, keyText: text}
}

// apiErrorBody is the Anthropic error envelope the stub returns for a non-2xx
// response. Shared by the tests that drive Do down its error path so the
// envelope's literals live in exactly one place.
func apiErrorBody(msg string) map[string]any {
	return map[string]any{
		keyType: "error",
		"error": map[string]any{keyType: "api_error", "message": msg},
	}
}

func toolUseBlock(id, name string, input map[string]any) map[string]any {
	return map[string]any{keyType: "tool_use", "id": id, "name": name, "input": input}
}

func TestDoMapsTextResponse(t *testing.T) {
	adapter, _ := newAdapterAgainst(t, brain.Config{Model: modelOverride}, http.StatusOK,
		message("end_turn", textBlock("all done")))

	resp, err := adapter.Do(context.Background(), brain.LLMRequest{
		System:   "sys",
		Messages: []brain.LLMMessage{{Role: brain.LLMRoleUser, Text: "hi"}},
	})
	if err != nil {
		t.Fatalf("Do: unexpected error: %v", err)
	}
	if resp.StopReason != brain.StopEndTurn {
		t.Errorf("StopReason = %q, want %q", resp.StopReason, brain.StopEndTurn)
	}
	if resp.Text != "all done" {
		t.Errorf("Text = %q, want %q", resp.Text, "all done")
	}
	if len(resp.Calls) != 0 {
		t.Errorf("Calls = %v, want none", resp.Calls)
	}
}

func TestDoMapsToolUseResponse(t *testing.T) {
	adapter, _ := newAdapterAgainst(t, brain.Config{Model: modelOverride}, http.StatusOK,
		message("tool_use",
			textBlock("thinking"),
			toolUseBlock(callID1, string(brain.ToolGetTicket), map[string]any{"id": ticketT1}),
		))

	resp, err := adapter.Do(context.Background(), brain.LLMRequest{})
	if err != nil {
		t.Fatalf("Do: unexpected error: %v", err)
	}
	if resp.StopReason != brain.StopToolUse {
		t.Errorf("StopReason = %q, want %q", resp.StopReason, brain.StopToolUse)
	}
	if resp.Text != "thinking" {
		t.Errorf("Text = %q, want %q", resp.Text, "thinking")
	}
	if len(resp.Calls) != 1 {
		t.Fatalf("len(Calls) = %d, want 1", len(resp.Calls))
	}
	got := resp.Calls[0]
	if got.ID != callID1 {
		t.Errorf("Calls[0].ID = %q, want %q", got.ID, callID1)
	}
	if got.Name != brain.ToolGetTicket {
		t.Errorf("Calls[0].Name = %q, want %q", got.Name, brain.ToolGetTicket)
	}
	var input struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(got.Input, &input); err != nil {
		t.Fatalf("unmarshal Calls[0].Input %q: %v", got.Input, err)
	}
	if input.ID != ticketT1 {
		t.Errorf("Calls[0].Input.id = %q, want %q", input.ID, ticketT1)
	}
}

// TestDoMapsUnhandledStopReasonToEndTurn covers fromSDKMessage's default: a
// stop reason this module has no handling for ends the pass. max_tokens is
// deliberately not one of them — see the test below.
func TestDoMapsUnhandledStopReasonToEndTurn(t *testing.T) {
	adapter, _ := newAdapterAgainst(t, brain.Config{Model: modelOverride}, http.StatusOK,
		message("refusal", textBlock("declined")))

	resp, err := adapter.Do(context.Background(), brain.LLMRequest{})
	if err != nil {
		t.Fatalf("Do: unexpected error: %v", err)
	}
	if resp.StopReason != brain.StopEndTurn {
		t.Errorf("StopReason = %q, want %q (an unhandled stop reason ends the pass)",
			resp.StopReason, brain.StopEndTurn)
	}
}

// TestDoMapsMaxTokensToItsOwnStopReason pins the distinction the pass loop runs
// on: a round cut off at the output ceiling must not come back wearing
// end_turn's clothes. Folded together, "the model finished" and "the model was
// stopped mid-call" were indistinguishable, and runPass returned from a
// truncated round as though the pass were done — taking the calls it carried
// with it, silently.
func TestDoMapsMaxTokensToItsOwnStopReason(t *testing.T) {
	adapter, _ := newAdapterAgainst(t, brain.Config{Model: modelOverride}, http.StatusOK,
		message("max_tokens",
			textBlock("cut off"),
			toolUseBlock(callID1, string(brain.ToolGetTicket), map[string]any{"id": ticketT1}),
		))

	resp, err := adapter.Do(context.Background(), brain.LLMRequest{})
	if err != nil {
		t.Fatalf("Do: unexpected error: %v", err)
	}
	if resp.StopReason != brain.StopMaxTokens {
		t.Fatalf("StopReason = %q, want %q — truncation must be distinguishable from a finished turn",
			resp.StopReason, brain.StopMaxTokens)
	}
	if len(resp.Calls) != 1 {
		t.Errorf("len(Calls) = %d, want 1 — a truncated round's calls still reach the loop", len(resp.Calls))
	}
}

// TestDoConcatenatesTextBlocks: fromSDKMessage sums every text block into Text.
func TestDoConcatenatesTextBlocks(t *testing.T) {
	adapter, _ := newAdapterAgainst(t, brain.Config{Model: modelOverride}, http.StatusOK,
		message("end_turn", textBlock("part one "), textBlock("part two")))

	resp, err := adapter.Do(context.Background(), brain.LLMRequest{})
	if err != nil {
		t.Fatalf("Do: unexpected error: %v", err)
	}
	if resp.Text != "part one part two" {
		t.Errorf("Text = %q, want %q", resp.Text, "part one part two")
	}
}

// TestDoSendsARoomyOutputCeiling guards the ceiling from drifting back down.
// The number itself is not the contract — the floor is: 4096 was low enough
// that a batched read round followed by a few send_to_agent instructions ran
// into it, and a round that hits the ceiling is a round the loop has to repair.
// Anything at or below the old value puts that back.
func TestDoSendsARoomyOutputCeiling(t *testing.T) {
	adapter, stub := newAdapterAgainst(t, brain.Config{Model: modelOverride}, http.StatusOK,
		message("end_turn", textBlock("ok")))

	if _, err := adapter.Do(context.Background(), brain.LLMRequest{}); err != nil {
		t.Fatalf("Do: unexpected error: %v", err)
	}

	const oldCeiling = 4096
	got, ok := stub.lastBody["max_tokens"].(float64)
	if !ok {
		t.Fatalf("request max_tokens = %v, want a number", stub.lastBody["max_tokens"])
	}
	if got <= oldCeiling {
		t.Errorf("request max_tokens = %v, want more than the old %d ceiling that truncated a real round",
			got, oldCeiling)
	}
}

// TestDoSendsSystemAndModel checks model() resolution (explicit Config.Model)
// and that a non-empty System becomes a system block on the wire.
func TestDoSendsSystemAndModel(t *testing.T) {
	adapter, stub := newAdapterAgainst(t, brain.Config{Model: modelOverride}, http.StatusOK,
		message("end_turn", textBlock("ok")))

	if _, err := adapter.Do(context.Background(), brain.LLMRequest{
		System:   "the fixed system prompt",
		Messages: []brain.LLMMessage{{Role: brain.LLMRoleUser, Text: sayHello}},
	}); err != nil {
		t.Fatalf("Do: unexpected error: %v", err)
	}

	if got := stub.lastBody["model"]; got != modelOverride {
		t.Errorf("request model = %v, want %q", got, modelOverride)
	}
	system := asSlice(t, "request system", stub.lastBody["system"])
	if len(system) != 1 {
		t.Fatalf("request system = %v, want one block", stub.lastBody["system"])
	}
	block := asMap(t, "system block", system[0])
	if block[keyText] != "the fixed system prompt" {
		t.Errorf("system block text = %v, want %q", block[keyText], "the fixed system prompt")
	}
	// The fixed prefix's breakpoint must carry the 1h TTL: passes are event-
	// driven and often more than 5m apart, so the default TTL made nearly
	// every pass a cold cache write (see Do's placement comment).
	cache := asMap(t, "system cache_control", block["cache_control"])
	if cache[keyType] != "ephemeral" {
		t.Errorf("system cache_control type = %v, want %q", cache[keyType], "ephemeral")
	}
	if cache["ttl"] != "1h" {
		t.Errorf("system cache_control ttl = %v, want %q", cache["ttl"], "1h")
	}
}

// TestDoDefaultsModelWhenUnset: with an empty Config.Model and no env override,
// model() falls back to DefaultModel.
func TestDoDefaultsModelWhenUnset(t *testing.T) {
	t.Setenv(brain.ModelEnvVar, "")
	adapter, stub := newAdapterAgainst(t, brain.Config{}, http.StatusOK,
		message("end_turn", textBlock("ok")))

	if _, err := adapter.Do(context.Background(), brain.LLMRequest{}); err != nil {
		t.Fatalf("Do: unexpected error: %v", err)
	}
	if got := stub.lastBody["model"]; got != brain.DefaultModel {
		t.Errorf("request model = %v, want default %q", got, brain.DefaultModel)
	}
}

// TestDoUsesModelEnvVarFallback: an empty Config.Model falls back to the
// KILN_BRAIN_MODEL env var before DefaultModel.
func TestDoUsesModelEnvVarFallback(t *testing.T) {
	t.Setenv(brain.ModelEnvVar, "claude-from-env")
	adapter, stub := newAdapterAgainst(t, brain.Config{}, http.StatusOK,
		message("end_turn", textBlock("ok")))

	if _, err := adapter.Do(context.Background(), brain.LLMRequest{}); err != nil {
		t.Fatalf("Do: unexpected error: %v", err)
	}
	if got := stub.lastBody["model"]; got != "claude-from-env" {
		t.Errorf("request model = %v, want %q", got, "claude-from-env")
	}
}

// outputConfigEffort reads output_config.effort off a captured request body,
// failing loudly when the block is missing — an absent effort means the API
// default (high) is back, which is the regression these tests exist to catch.
func outputConfigEffort(t *testing.T, stub *anthropicStub) any {
	t.Helper()
	oc := asMap(t, "request output_config", stub.lastBody["output_config"])
	effort, present := oc["effort"]
	if !present {
		t.Fatalf("request output_config = %v, want an effort", oc)
	}
	return effort
}

// TestDoDefaultsEffortWhenUnset: with an empty Config.Effort and no env
// override, effort() falls back to DefaultEffort — so a round never silently
// runs at the API's "high" default (06 §2).
func TestDoDefaultsEffortWhenUnset(t *testing.T) {
	t.Setenv(brain.EffortEnvVar, "")
	adapter, stub := newAdapterAgainst(t, brain.Config{Model: modelOverride}, http.StatusOK,
		message("end_turn", textBlock("ok")))

	if _, err := adapter.Do(context.Background(), brain.LLMRequest{}); err != nil {
		t.Fatalf("Do: unexpected error: %v", err)
	}
	if got := outputConfigEffort(t, stub); got != string(brain.DefaultEffort) {
		t.Errorf("request output_config.effort = %v, want default %q", got, brain.DefaultEffort)
	}
}

// TestDoUsesConfiguredEffort: Config.Effort reaches the wire verbatim.
func TestDoUsesConfiguredEffort(t *testing.T) {
	t.Setenv(brain.EffortEnvVar, "")
	adapter, stub := newAdapterAgainst(t,
		brain.Config{Model: modelOverride, Effort: brain.EffortLow}, http.StatusOK,
		message("end_turn", textBlock("ok")))

	if _, err := adapter.Do(context.Background(), brain.LLMRequest{}); err != nil {
		t.Fatalf("Do: unexpected error: %v", err)
	}
	if got := outputConfigEffort(t, stub); got != string(brain.EffortLow) {
		t.Errorf("request output_config.effort = %v, want %q", got, brain.EffortLow)
	}
}

// TestDoUsesEffortEnvVarFallback: an empty Config.Effort falls back to the
// KILN_BRAIN_EFFORT env var before DefaultEffort.
func TestDoUsesEffortEnvVarFallback(t *testing.T) {
	t.Setenv(brain.EffortEnvVar, string(brain.EffortHigh))
	adapter, stub := newAdapterAgainst(t, brain.Config{Model: modelOverride}, http.StatusOK,
		message("end_turn", textBlock("ok")))

	if _, err := adapter.Do(context.Background(), brain.LLMRequest{}); err != nil {
		t.Fatalf("Do: unexpected error: %v", err)
	}
	if got := outputConfigEffort(t, stub); got != string(brain.EffortHigh) {
		t.Errorf("request output_config.effort = %v, want %q", got, brain.EffortHigh)
	}
}

// TestDoOmitsSystemWhenEmpty: an empty System must not put a system block on
// the wire.
func TestDoOmitsSystemWhenEmpty(t *testing.T) {
	adapter, stub := newAdapterAgainst(t, brain.Config{Model: modelOverride}, http.StatusOK,
		message("end_turn", textBlock("ok")))

	if _, err := adapter.Do(context.Background(), brain.LLMRequest{}); err != nil {
		t.Fatalf("Do: unexpected error: %v", err)
	}
	if _, present := stub.lastBody["system"]; present {
		t.Errorf("request should omit system when empty, got %v", stub.lastBody["system"])
	}
}

// TestDoEncodesConversation exercises toSDKMessages for both roles: an
// assistant turn carrying text + a tool_use block, then a user turn carrying a
// tool_result block. It asserts the exact wire shape the SDK produced.
func TestDoEncodesConversation(t *testing.T) {
	adapter, stub := newAdapterAgainst(t, brain.Config{Model: modelOverride}, http.StatusOK,
		message("end_turn", textBlock("ok")))

	req := brain.LLMRequest{
		Messages: []brain.LLMMessage{
			{Role: brain.LLMRoleUser, Text: "the context block"},
			{
				Role:  brain.LLMRoleAssistant,
				Text:  "I'll mark it ready",
				Calls: []brain.ToolCall{{ID: callID1, Name: brain.ToolGetTicket, Input: json.RawMessage(`{"id":"t-1"}`)}},
			},
			{
				Role:    brain.LLMRoleUser,
				Results: []brain.ToolResult{{ToolCallID: callID1, Content: "ok", IsError: false}},
			},
		},
	}
	if _, err := adapter.Do(context.Background(), req); err != nil {
		t.Fatalf("Do: unexpected error: %v", err)
	}

	msgs := asSlice(t, "request messages", stub.lastBody["messages"])
	if len(msgs) != 3 {
		t.Fatalf("request messages = %v, want 3", stub.lastBody["messages"])
	}

	// Message 0: plain user text block.
	first := asMap(t, "messages[0]", msgs[0])
	if first["role"] != "user" {
		t.Errorf("messages[0].role = %v, want user", first["role"])
	}
	firstBlocks := asSlice(t, "messages[0].content", first["content"])
	if len(firstBlocks) != 1 {
		t.Fatalf("messages[0].content = %v, want 1 block", first["content"])
	}
	fb := asMap(t, "messages[0] block", firstBlocks[0])
	if fb[keyType] != keyText || fb[keyText] != "the context block" {
		t.Errorf("messages[0] block = %v, want text 'the context block'", fb)
	}

	// Message 1: assistant text + tool_use.
	asst := asMap(t, "messages[1]", msgs[1])
	if asst["role"] != "assistant" {
		t.Errorf("messages[1].role = %v, want assistant", asst["role"])
	}
	asstBlocks := asSlice(t, "messages[1].content", asst["content"])
	if len(asstBlocks) != 2 {
		t.Fatalf("messages[1].content = %v, want 2 blocks (text + tool_use)", asst["content"])
	}
	b0 := asMap(t, "messages[1] block 0", asstBlocks[0])
	if b0[keyType] != keyText || b0[keyText] != "I'll mark it ready" {
		t.Errorf("messages[1] block 0 = %v, want assistant text", asstBlocks[0])
	}
	tu := asMap(t, "messages[1] block 1", asstBlocks[1])
	if tu[keyType] != "tool_use" || tu["id"] != callID1 || tu["name"] != string(brain.ToolGetTicket) {
		t.Errorf("messages[1] block 1 = %v, want tool_use get_ticket call-1", tu)
	}
	if input := asMap(t, "messages[1] tool_use input", tu["input"]); input["id"] != ticketT1 {
		t.Errorf("messages[1] tool_use input = %v, want id t-1", tu["input"])
	}

	// Message 2: user tool_result.
	usr := asMap(t, "messages[2]", msgs[2])
	usrBlocks := asSlice(t, "messages[2].content", usr["content"])
	if len(usrBlocks) != 1 {
		t.Fatalf("messages[2].content = %v, want 1 block", usr["content"])
	}
	tr := asMap(t, "messages[2] block", usrBlocks[0])
	if tr[keyType] != "tool_result" || tr["tool_use_id"] != callID1 {
		t.Errorf("messages[2] block = %v, want tool_result for call-1", tr)
	}
}

// TestDoEncodesTools exercises toSDKTools: the fixed tool set reaches the wire
// with each tool's name, description, and input schema (properties + required).
func TestDoEncodesTools(t *testing.T) {
	adapter, stub := newAdapterAgainst(t, brain.Config{Model: modelOverride}, http.StatusOK,
		message("end_turn", textBlock("ok")))

	if _, err := adapter.Do(context.Background(), brain.LLMRequest{Tools: brain.Tools}); err != nil {
		t.Fatalf("Do: unexpected error: %v", err)
	}

	tools := asSlice(t, "request tools", stub.lastBody["tools"])
	if len(tools) != len(brain.Tools) {
		t.Fatalf("request tools = %v, want %d", stub.lastBody["tools"], len(brain.Tools))
	}

	byName := make(map[string]map[string]any, len(tools))
	for _, tv := range tools {
		tm := asMap(t, "tool", tv)
		name, ok := tm["name"].(string)
		if !ok {
			t.Fatalf("tool name = %v, want string", tm["name"])
		}
		byName[name] = tm
	}

	for _, def := range brain.Tools {
		wire, present := byName[string(def.Name)]
		if !present {
			t.Errorf("tool %q missing from request", def.Name)
			continue
		}
		if wire["description"] != def.Description {
			t.Errorf("tool %q description = %v, want %q", def.Name, wire["description"], def.Description)
		}
		schema := asMap(t, "tool input_schema", wire["input_schema"])
		if schema[keyType] != "object" {
			t.Errorf("tool %q input_schema.type = %v, want object", def.Name, schema[keyType])
		}
		if _, hasProps := schema["properties"]; !hasProps {
			t.Errorf("tool %q input_schema missing properties", def.Name)
		}
	}
}

// TestDoWrapsAPIError: a non-2xx response surfaces as a wrapped, non-nil error
// and an empty LLMResponse.
func TestDoWrapsAPIError(t *testing.T) {
	adapter, _ := newAdapterAgainst(t, brain.Config{Model: modelOverride}, http.StatusInternalServerError,
		apiErrorBody("boom"))

	resp, err := adapter.Do(context.Background(), brain.LLMRequest{})
	if err == nil {
		t.Fatalf("Do: expected error on HTTP 500, got nil (resp %+v)", resp)
	}
	if resp.StopReason != "" || resp.Text != "" || len(resp.Calls) != 0 {
		t.Errorf("Do: expected zero LLMResponse on error, got %+v", resp)
	}
}

// captureRounds installs a JSON handler as the default logger for the duration
// of the test and returns an accessor for the decoded "brain: llm round"
// records emitted while it is installed. The Adapter logs through
// slog.Default() when no logger is injected (llm.go's log()), so this is how
// the round record is observable from outside the package. Safe because no test
// in this package runs in parallel.
func captureRounds(t *testing.T) func() []map[string]any {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	return func() []map[string]any {
		var rounds []map[string]any
		for line := range strings.SplitSeq(buf.String(), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var rec map[string]any
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				t.Fatalf("decode log record %q: %v", line, err)
			}
			if rec["msg"] == "brain: llm round" {
				rounds = append(rounds, rec)
			}
		}
		return rounds
	}
}

// TestDoLogsRoundOutputAlongsideUsage: the per-round record carries the model's
// own output — text, tool calls, stop reason — next to the token counts, so a
// pass is readable in the trace and not just costable.
func TestDoLogsRoundOutputAlongsideUsage(t *testing.T) {
	rounds := captureRounds(t)
	adapter, _ := newAdapterAgainst(t, brain.Config{Model: modelOverride}, http.StatusOK,
		message("tool_use",
			textBlock("marking it ready"),
			toolUseBlock(callID1, string(brain.ToolGetTicket), map[string]any{"id": ticketT1}),
		))

	if _, err := adapter.Do(context.Background(), brain.LLMRequest{}); err != nil {
		t.Fatalf("Do: unexpected error: %v", err)
	}

	got := rounds()
	if len(got) != 1 {
		t.Fatalf("logged %d round records, want 1", len(got))
	}
	rec := got[0]
	if rec["text"] != "marking it ready" {
		t.Errorf("round text = %v, want the assistant text", rec["text"])
	}
	wantCalls := string(brain.ToolGetTicket) + "#" + callID1
	if rec["tool_calls"] != wantCalls {
		t.Errorf("round tool_calls = %v, want %q", rec["tool_calls"], wantCalls)
	}
	if rec["stop_reason"] != string(brain.StopToolUse) {
		t.Errorf("round stop_reason = %v, want %q", rec["stop_reason"], brain.StopToolUse)
	}
	// The usage attributes the record already carried must survive alongside it.
	for _, key := range []string{
		keyInputTokens, keyOutputTokens,
		keyCacheRead, keyCacheCreation,
	} {
		if _, present := rec[key]; !present {
			t.Errorf("round record dropped usage attribute %q: %v", key, rec)
		}
	}
}

// TestDoLogsCacheWriteTTLSplit: a 5m cache write bills at 1.25× the base input
// rate and a 1h write at 2×, so the aggregate alone leaves a round's cost a
// range. The record carries the per-TTL counts the API reports, and still
// carries every usage figure it did before, with the values it was given.
func TestDoLogsCacheWriteTTLSplit(t *testing.T) {
	rounds := captureRounds(t)
	adapter, _ := newAdapterAgainst(t, brain.Config{Model: modelOverride}, http.StatusOK,
		messageWithUsage("end_turn", map[string]any{
			keyInputTokens:   310,
			keyOutputTokens:  84,
			keyCacheRead:     4200,
			keyCacheCreation: 2100,
			// The nested block is the API's own shape; the flat
			// cache_creation_* attrs above are what logRound makes of it.
			"cache_creation": map[string]any{
				"ephemeral_5m_input_tokens": 600,
				"ephemeral_1h_input_tokens": 1500,
			},
		}, textBlock("nothing to do")))

	if _, err := adapter.Do(context.Background(), brain.LLMRequest{}); err != nil {
		t.Fatalf("Do: unexpected error: %v", err)
	}

	got := rounds()
	if len(got) != 1 {
		t.Fatalf("logged %d round records, want 1", len(got))
	}
	// Decoded JSON numbers land as float64.
	for key, want := range map[string]float64{
		keyInputTokens:     310,
		keyOutputTokens:    84,
		keyCacheRead:       4200,
		keyCacheCreation:   2100,
		keyCacheCreation5m: 600,
		keyCacheCreation1h: 1500,
	} {
		if got[0][key] != want {
			t.Errorf("round %s = %v, want %v", key, got[0][key], want)
		}
	}
}

// TestDoLogsZeroCacheWriteSplitOnAReadOnlyRound: a round that writes no cache
// still reports both TTL counts, at zero, so a consumer summing them across
// rounds never has to distinguish "no write" from "attribute missing".
func TestDoLogsZeroCacheWriteSplitOnAReadOnlyRound(t *testing.T) {
	rounds := captureRounds(t)
	adapter, _ := newAdapterAgainst(t, brain.Config{Model: modelOverride}, http.StatusOK,
		messageWithUsage("end_turn", map[string]any{
			keyInputTokens:  12,
			keyOutputTokens: 7,
			keyCacheRead:    4200,
		}, textBlock("nothing to do")))

	if _, err := adapter.Do(context.Background(), brain.LLMRequest{}); err != nil {
		t.Fatalf("Do: unexpected error: %v", err)
	}

	got := rounds()
	if len(got) != 1 {
		t.Fatalf("logged %d round records, want 1", len(got))
	}
	for _, key := range []string{keyCacheCreation5m, keyCacheCreation1h} {
		if got[0][key] != float64(0) {
			t.Errorf("round %s = %v, want 0", key, got[0][key])
		}
	}
}

// TestDoLogsTextOnlyRound: a round that ends the pass with text and no tools
// still reaches the trace — the case brain.tool's per-call records never cover.
func TestDoLogsTextOnlyRound(t *testing.T) {
	rounds := captureRounds(t)
	adapter, _ := newAdapterAgainst(t, brain.Config{Model: modelOverride}, http.StatusOK,
		message("end_turn", textBlock("nothing to do")))

	if _, err := adapter.Do(context.Background(), brain.LLMRequest{}); err != nil {
		t.Fatalf("Do: unexpected error: %v", err)
	}

	got := rounds()
	if len(got) != 1 {
		t.Fatalf("logged %d round records, want 1", len(got))
	}
	if got[0]["text"] != "nothing to do" {
		t.Errorf("round text = %v, want the final text", got[0]["text"])
	}
	if got[0]["tool_calls"] != "" {
		t.Errorf("round tool_calls = %v, want empty for a text-only round", got[0]["tool_calls"])
	}
}

// TestDoDoesNotLogRoundOnAPIError: a failed call has no model output to trace,
// so it must not emit a round record (the wrapped error is the signal).
func TestDoDoesNotLogRoundOnAPIError(t *testing.T) {
	rounds := captureRounds(t)
	adapter, _ := newAdapterAgainst(t, brain.Config{Model: modelOverride}, http.StatusInternalServerError,
		apiErrorBody("boom"))

	if _, err := adapter.Do(context.Background(), brain.LLMRequest{}); err == nil {
		t.Fatal("Do: expected error on HTTP 500, got nil")
	}
	if got := rounds(); len(got) != 0 {
		t.Errorf("logged %d round records on API error, want 0", len(got))
	}
}

// TestConstructorsImplementLLM: both constructors return a usable, non-nil
// Adapter that satisfies the LLM port.
func TestConstructorsImplementLLM(t *testing.T) {
	var _ brain.LLM = brain.NewAdapter(brain.Config{Model: modelOverride})
	var _ brain.LLM = brain.NewAdapterWithClient(brain.Config{Model: modelOverride}, option.WithAPIKey(testAPIKey))

	if a := brain.NewAdapter(brain.Config{}); a == nil {
		t.Fatal("NewAdapter returned nil")
	}
	if a := brain.NewAdapterWithClient(brain.Config{}); a == nil {
		t.Fatal("NewAdapterWithClient returned nil")
	}
}
