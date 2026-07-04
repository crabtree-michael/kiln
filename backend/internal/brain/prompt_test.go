package brain_test

// TestRenderSystemPrompt_V4_MentionsAgentReadTools pins the v4 prompt content
// added in this task: the brain must be told it can observe agents directly
// via list_agents and get_agent_updates (05 §2, 06 §4 amended).

import (
	"strings"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/brain"
)

func TestRenderSystemPrompt_V4_MentionsAgentReadTools(t *testing.T) {
	out, err := brain.RenderSystemPrompt(brain.CurrentPromptVersion, brain.PromptData{Role: "the orchestrator"})
	if err != nil {
		t.Fatalf("RenderSystemPrompt: %v", err)
	}
	if !strings.Contains(out, "list_agents") || !strings.Contains(out, "get_agent_updates") {
		t.Errorf("v4 prompt missing agent read-tool guidance:\n%s", out)
	}
}
