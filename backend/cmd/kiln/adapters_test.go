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

// TestNotifyURL pins the tap-to-open deep link (02 §10, 12 §6.3): the notify.send
// lands on `/app?project=<id>[&ticket=<id>]` — the project param deep-links the
// tap to the firing project (so a tap never opens a different tenant, 12 §6.3),
// and the ticket param opens that proposal. Both empty falls back to the plain
// app root (the primary screen lives at `/app`; `/` is the marketing page).
func TestNotifyURL(t *testing.T) {
	cases := []struct {
		name      string
		projectID string
		id        board.TicketID
		want      string
	}{
		{name: "project + ticket", projectID: "proj-9", id: "t-123", want: "/app?project=proj-9&ticket=t-123"},
		{name: "project only", projectID: "proj-9", id: "", want: "/app?project=proj-9"},
		{name: "both empty falls back to root", projectID: "", id: "", want: "/app"},
		{name: "escaping", projectID: "p 1", id: "a b/c", want: "/app?project=p+1&ticket=a+b%2Fc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := notifyURL(tc.projectID, tc.id); got != tc.want {
				t.Fatalf("notifyURL(%q, %q) = %q, want %q", tc.projectID, tc.id, got, tc.want)
			}
		})
	}
}
