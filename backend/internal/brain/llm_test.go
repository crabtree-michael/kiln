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
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/crabtree-michael/kiln/backend/internal/brain"
)

const (
	testAPIKey    = "test-key"
	pathMessages  = "/v1/messages"
	modelOverride = "claude-test-model"
	callID1       = "call-1"
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
		"type":          "message",
		"role":          "assistant",
		"model":         modelOverride,
		"content":       content,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage":         map[string]any{"input_tokens": 1, "output_tokens": 1},
	}
}

func textBlock(text string) map[string]any {
	return map[string]any{"type": "text", "text": text}
}

func toolUseBlock(id, name string, input map[string]any) map[string]any {
	return map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}
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
			toolUseBlock(callID1, string(brain.ToolMarkReady), map[string]any{"id": "t-1"}),
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
	if got.Name != brain.ToolMarkReady {
		t.Errorf("Calls[0].Name = %q, want %q", got.Name, brain.ToolMarkReady)
	}
	var input struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(got.Input, &input); err != nil {
		t.Fatalf("unmarshal Calls[0].Input %q: %v", got.Input, err)
	}
	if input.ID != "t-1" {
		t.Errorf("Calls[0].Input.id = %q, want %q", input.ID, "t-1")
	}
}

// TestDoMapsNonToolUseStopReasonToEndTurn covers fromSDKMessage's rule that
// only tool_use continues the loop; any other stop reason ends the pass.
func TestDoMapsNonToolUseStopReasonToEndTurn(t *testing.T) {
	adapter, _ := newAdapterAgainst(t, brain.Config{Model: modelOverride}, http.StatusOK,
		message("max_tokens", textBlock("cut off")))

	resp, err := adapter.Do(context.Background(), brain.LLMRequest{})
	if err != nil {
		t.Fatalf("Do: unexpected error: %v", err)
	}
	if resp.StopReason != brain.StopEndTurn {
		t.Errorf("StopReason = %q, want %q (non-tool_use ends the pass)", resp.StopReason, brain.StopEndTurn)
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

// TestDoSendsSystemAndModel checks model() resolution (explicit Config.Model)
// and that a non-empty System becomes a system block on the wire.
func TestDoSendsSystemAndModel(t *testing.T) {
	adapter, stub := newAdapterAgainst(t, brain.Config{Model: modelOverride}, http.StatusOK,
		message("end_turn", textBlock("ok")))

	if _, err := adapter.Do(context.Background(), brain.LLMRequest{
		System:   "the fixed system prompt",
		Messages: []brain.LLMMessage{{Role: brain.LLMRoleUser, Text: "hello"}},
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
	if block["text"] != "the fixed system prompt" {
		t.Errorf("system block text = %v, want %q", block["text"], "the fixed system prompt")
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
				Calls: []brain.ToolCall{{ID: callID1, Name: brain.ToolMarkReady, Input: json.RawMessage(`{"id":"t-1"}`)}},
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
	if fb["type"] != "text" || fb["text"] != "the context block" {
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
	if b0 := asMap(t, "messages[1] block 0", asstBlocks[0]); b0["type"] != "text" || b0["text"] != "I'll mark it ready" {
		t.Errorf("messages[1] block 0 = %v, want assistant text", asstBlocks[0])
	}
	tu := asMap(t, "messages[1] block 1", asstBlocks[1])
	if tu["type"] != "tool_use" || tu["id"] != callID1 || tu["name"] != string(brain.ToolMarkReady) {
		t.Errorf("messages[1] block 1 = %v, want tool_use mark_ready call-1", tu)
	}
	if input := asMap(t, "messages[1] tool_use input", tu["input"]); input["id"] != "t-1" {
		t.Errorf("messages[1] tool_use input = %v, want id t-1", tu["input"])
	}

	// Message 2: user tool_result.
	usr := asMap(t, "messages[2]", msgs[2])
	usrBlocks := asSlice(t, "messages[2].content", usr["content"])
	if len(usrBlocks) != 1 {
		t.Fatalf("messages[2].content = %v, want 1 block", usr["content"])
	}
	tr := asMap(t, "messages[2] block", usrBlocks[0])
	if tr["type"] != "tool_result" || tr["tool_use_id"] != callID1 {
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
		if schema["type"] != "object" {
			t.Errorf("tool %q input_schema.type = %v, want object", def.Name, schema["type"])
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
		map[string]any{"type": "error", "error": map[string]any{"type": "api_error", "message": "boom"}})

	resp, err := adapter.Do(context.Background(), brain.LLMRequest{})
	if err == nil {
		t.Fatalf("Do: expected error on HTTP 500, got nil (resp %+v)", resp)
	}
	if resp.StopReason != "" || resp.Text != "" || len(resp.Calls) != 0 {
		t.Errorf("Do: expected zero LLMResponse on error, got %+v", resp)
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
