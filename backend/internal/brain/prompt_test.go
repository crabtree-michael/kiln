package brain

import (
	"strings"
	"testing"
)

// testRole is the Role every prompt-rendering test renders against; the value
// is immaterial to what these tests assert, only its presence in the template.
const testRole = "the orchestrator"

// The two merge gates every prompt rule is checked under — a rule that survives
// only one of them is not pinned. Hoisted to consts so no literal recurs 3+
// times across the file (goconst), as fakes_test.go does for brain_test.
const (
	subtestMainGate = "main gate"
	subtestPRGate   = "pr gate"
)

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
		{subtestMainGate, false},
		{subtestPRGate, true},
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

// TestRenderSystemPrompt_ReadsOnceAndTakesRefusalsAsFinal pins the prose half of
// the two repeat fixes. 4.1% of all measured tool calls were an exact repeat
// inside one pass, and 10.3% of update_ticket calls still fail on a transition
// the state does not accept, clustering on single ids because the model
// re-issues the refused edit (docs/brain-optimization-2026-08-08-measured.md
// §10, §11). The memo (memo.go) makes a repeat cheap; only this can stop it
// being emitted, and neither is visible to the scripted-fake suite — so what is
// pinned here is that the reasoning is still stated, under either merge gate.
func TestRenderSystemPrompt_ReadsOnceAndTakesRefusalsAsFinal(t *testing.T) {
	for _, tc := range []struct {
		name     string
		doneInPR bool
	}{
		{subtestMainGate, false},
		{subtestPRGate, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RenderSystemPrompt(PromptData{Role: testRole, DoneInPR: tc.doneInPR})
			if err != nil {
				t.Fatalf("RenderSystemPrompt: %v", err)
			}
			for _, want := range []string{
				// The rule, and the reason it holds: a result already given is
				// still in the conversation, and nothing moves the board but the
				// pass itself.
				"Ask for a given read once",
				"still in front of you",
				"Nothing moves the board while you are thinking",
				// The one legitimate re-read, so the rule does not read as "never".
				"only after an action of",
				// §11: what the working-ticket edit was reaching for, and where
				// that actually goes.
				"send_to_agent is what actually delivers it",
				// …and that a refusal is not worth a second attempt.
				"Take a refusal as final",
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
		{subtestMainGate, false},
		{subtestPRGate, true},
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

// TestRenderSystemPrompt_PendingMergeIsNotABlocker pins the third silence, which
// is a state change rather than a channel: blocked fires a push notification (10),
// so blocking a ticket whose only outstanding item is "the agent has yet to land
// its work" wakes the user for coordination already under way and already being
// handled by the nudge. The rule has to survive under either merge gate, and the
// gate section itself — which used to prescribe exactly this block — has to agree
// with it.
func TestRenderSystemPrompt_PendingMergeIsNotABlocker(t *testing.T) {
	for _, tc := range []struct {
		name     string
		doneInPR bool
	}{
		{subtestMainGate, false},
		{subtestPRGate, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RenderSystemPrompt(PromptData{Role: testRole, DoneInPR: tc.doneInPR})
			if err != nil {
				t.Fatalf("RenderSystemPrompt: %v", err)
			}
			for _, want := range []string{
				// The rule, and why blocked is governed by the same principle as
				// the announcement channels: it costs the user a notification.
				"A pending merge is not a blocker.",
				"Do NOT set it blocked",
				"leave the ticket in the state it is already in",
				// What to do instead, and the one thing that does earn a block.
				"send_to_agent",
				"Escalate to blocked only once the nudge has failed",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("prompt should contain %q:\n%s", want, got)
				}
			}
			// The superseded prescription: the gate section told the model to
			// block the ticket while the agent landed the work, which is the
			// exact pattern the rule above rules out.
			if strings.Contains(got, "set the\nticket blocked meanwhile") {
				t.Errorf("gate section still prescribes blocking for a pending merge:\n%s", got)
			}
		})
	}
}
