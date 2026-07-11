package main

import (
	"encoding/json"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
	"github.com/crabtree-michael/kiln/backend/internal/board"
)

// TestWithProjectID pins the fix for the stuck-agent-turn incident (11 §3):
// board's SendPayload/ReleasePayload never carry project_id, so
// agentRuntimeAdapter must stamp the runtime-supplied projectID onto the raw
// outbox payload before it reaches *agent.Service, or every turn's provider
// resolution fails on an empty project id.
func TestWithProjectID(t *testing.T) {
	raw, err := json.Marshal(board.SendPayload{TicketID: "t-1", WorkerID: "w-1", Message: "go"})
	if err != nil {
		t.Fatalf("marshal board payload: %v", err)
	}

	stamped, err := withProjectID(raw, "proj-123")
	if err != nil {
		t.Fatalf("withProjectID: %v", err)
	}

	var got agent.SendPayload
	if err := json.Unmarshal(stamped, &got); err != nil {
		t.Fatalf("unmarshal stamped payload: %v", err)
	}
	want := agent.SendPayload{ProjectID: "proj-123", TicketID: "t-1", WorkerID: "w-1", Message: "go"}
	if got != want {
		t.Fatalf("withProjectID() = %+v, want %+v", got, want)
	}
}

// TestNotifyURL pins the tap-to-open deep link (02 §10): a ticket-bearing
// notify.send lands on `/app?ticket=<id>` so the frontend opens that proposal
// (the primary screen lives at `/app`; `/` is the marketing landing page), and a
// ticketless payload falls back to the plain app root.
func TestNotifyURL(t *testing.T) {
	cases := []struct {
		name string
		id   board.TicketID
		want string
	}{
		{name: "ticket", id: "t-123", want: "/app?ticket=t-123"},
		{name: "empty falls back to root", id: "", want: "/app"},
		{name: "id needing escaping", id: "a b/c", want: "/app?ticket=a+b%2Fc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := notifyURL(tc.id); got != tc.want {
				t.Fatalf("notifyURL(%q) = %q, want %q", tc.id, got, tc.want)
			}
		})
	}
}
