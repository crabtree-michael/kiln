package brain

import (
	"strings"
	"testing"
)

// testRole is the Role every prompt-rendering test renders against; the value
// is immaterial to what these tests assert, only its presence in the template.
const testRole = "the orchestrator"

// TestRenderSystemPrompt_GateWording checks that the "What Counts As Done"
// section tracks the project's merge gate (06 §7): the default (DoneInPR=false)
// speaks of merging to origin/main, while the pull-request gate speaks of a PR
// and must NOT demand a merge to main.
func TestRenderSystemPrompt_GateWording(t *testing.T) {
	main, err := RenderSystemPrompt(PromptData{Role: testRole, DoneInPR: false})
	if err != nil {
		t.Fatalf("render main gate: %v", err)
	}
	if !strings.Contains(main, "merged to origin/main") {
		t.Errorf("main-gate prompt should require merge to origin/main:\n%s", main)
	}
	if strings.Contains(main, "in a pull request") {
		t.Errorf("main-gate prompt should not mention the pull-request gate:\n%s", main)
	}

	pr, err := RenderSystemPrompt(PromptData{Role: testRole, DoneInPR: true})
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

// TestRenderSystemPrompt_BatchesReadsIntoOneRound pins the round-batching rule
// under both gates. It is prose, but it is load-bearing prose: cache writes are
// ~50% of brain spend and every round rewrites the whole conversation prefix, so
// rounds-per-pass — not payload size — is the cost variable
// (docs/brain-optimization-2026-08-08-measured.md §1, §7). 11–14% of all rounds
// measured were a read round that could have been folded into the one before it,
// and nothing in the prompt had ever told the model it may ask for several reads
// at once. This asserts the instruction is still there, and still concrete about
// the opening round, where 84% of passes spend the wasted one.
func TestRenderSystemPrompt_BatchesReadsIntoOneRound(t *testing.T) {
	for _, tc := range []struct {
		name     string
		doneInPR bool
	}{
		{"main gate", false},
		{"pr gate", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RenderSystemPrompt(PromptData{Role: testRole, DoneInPR: tc.doneInPR})
			if err != nil {
				t.Fatalf("RenderSystemPrompt: %v", err)
			}
			for _, want := range []string{
				// The section, and why rounds are the thing being spent.
				"## Rounds",
				"every round re-sends this",
				// The rule itself: many calls per round, one round for all of them.
				"ask for everything you already know you need",
				// The opening round named concretely, plus the counter-example —
				// abstract "batch your reads" is what the model was already
				// failing to infer from nothing.
				"Most passes open with reads",
				"one round of three reads",
				// The guard against the rule backfiring into extra reads.
				"not licence to read more than you need",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("prompt should contain %q:\n%s", want, got)
				}
			}
		})
	}
}

// TestRenderSystemPrompt_NoRedundantNarration pins the two silences the brain
// kept breaking (08 §4, §7): a send_to_agent nudge to get a ticket's work landed
// is routine coordination the mechanical toast already covers, and a done ticket
// renders its own completion card, so neither warrants a post_update. Both are
// stated as one principle under "What Not To Announce" and must survive under
// either merge gate.
func TestRenderSystemPrompt_NoRedundantNarration(t *testing.T) {
	for _, tc := range []struct {
		name     string
		doneInPR bool
	}{
		{"main gate", false},
		{"pr gate", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RenderSystemPrompt(PromptData{Role: testRole, DoneInPR: tc.doneInPR})
			if err != nil {
				t.Fatalf("RenderSystemPrompt: %v", err)
			}
			for _, want := range []string{
				// The general principle, stated once and pointed at from the gate.
				"What Not To Announce",
				"Routine coordination is silent.",
				"A done ticket announces itself.",
				// The concrete "don't say this" examples that make the pattern
				// recognizable rather than abstract.
				"Nudging the GitHub App agent to commit its step 1 work",
				"Ticket 4 is done, merged to main at commit a1b2c3d.",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("prompt should contain %q:\n%s", want, got)
				}
			}
			// The old, too-narrow wording read as "don't narrate the act of
			// sending the message" and left reporting it afterwards open.
			if strings.Contains(got, "Do not inform the user when you message an agent") {
				t.Errorf("prompt still carries the superseded narrow caveat:\n%s", got)
			}
		})
	}
}
