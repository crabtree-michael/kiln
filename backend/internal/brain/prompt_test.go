package brain

import (
	"strings"
	"testing"
)

// TestRenderSystemPrompt_GateWording checks that the "What Counts As Done"
// section tracks the project's merge gate (06 §7): the default (DoneInPR=false)
// speaks of merging to origin/main, while the pull-request gate speaks of a PR
// and must NOT demand a merge to main.
func TestRenderSystemPrompt_GateWording(t *testing.T) {
	main, err := RenderSystemPrompt(PromptData{Role: "the orchestrator", DoneInPR: false})
	if err != nil {
		t.Fatalf("render main gate: %v", err)
	}
	if !strings.Contains(main, "merged to origin/main") {
		t.Errorf("main-gate prompt should require merge to origin/main:\n%s", main)
	}
	if strings.Contains(main, "in a pull request") {
		t.Errorf("main-gate prompt should not mention the pull-request gate:\n%s", main)
	}

	pr, err := RenderSystemPrompt(PromptData{Role: "the orchestrator", DoneInPR: true})
	if err != nil {
		t.Fatalf("render pr gate: %v", err)
	}
	if !strings.Contains(pr, "pull request") {
		t.Errorf("pr-gate prompt should mention a pull request:\n%s", pr)
	}
	if !strings.Contains(pr, "need NOT be merged to main") {
		t.Errorf("pr-gate prompt should state a merge to main is not required:\n%s", pr)
	}
}
