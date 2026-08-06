package brain_test

// The "allowed now" line on the two board reads: get_ticket and list_tickets
// report what a ticket's current state actually accepts, so the model checks
// instead of guessing at a transition and collecting an ErrInvalidTransition
// (docs/brain-optimization-2026-08-05.md §2). The idempotency half is pinned
// here too, by its absence: a refused transition still comes back verbatim.

import (
	"context"
	"strings"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/board"
	"github.com/crabtree-michael/kiln/backend/internal/brain"
)

const allowedMarker = "allowed now"

// TestGetTicket_ReportsWhatTheStateAllows walks all five states: each one's
// detail names the calls that state takes and none of the calls it refuses.
// The working and blocked rows are the ones that matter most — editing a
// working ticket's fields and re-queueing a working/blocked ticket were 57 of
// update_ticket's 105 failures in the window.
func TestGetTicket_ReportsWhatTheStateAllows(t *testing.T) {
	cases := []struct {
		state   board.State
		want    []string
		refused []string
	}{
		{
			state: board.StateShaping,
			want: []string{
				"update_ticket title/body/priority",
				"update_ticket approval_requested=true",
				"update_ticket " + argStateReady,
				toolNameDeleteTicket,
			},
			refused: []string{toolNameSendToAgent, `state="blocked"`, argStateDone},
		},
		{
			state: board.StateReady,
			want:  []string{"update_ticket title/body/priority", toolNameDeleteTicket},
			refused: []string{
				argStateReady, toolNameSendToAgent, "approval_requested=true", argStateDone,
			},
		},
		{
			state:   board.StateWorking,
			want:    []string{toolNameSendToAgent, `update_ticket state="blocked"`, "update_ticket " + argStateDone},
			refused: []string{"title/body/priority", argStateReady, toolNameDeleteTicket},
		},
		{
			state:   board.StateBlocked,
			want:    []string{toolNameSendToAgent, "update_ticket " + argStateDone, toolNameDeleteTicket},
			refused: []string{"title/body/priority", argStateReady, `state="blocked"`},
		},
		{
			state:   board.StateDone,
			want:    []string{toolNameDeleteTicket},
			refused: []string{toolNameSendToAgent, "update_ticket"},
		},
	}

	for _, tc := range cases {
		t.Run(string(tc.state), func(t *testing.T) {
			got := getTicketDetail(t, board.Ticket{ID: ticketT1, Title: "T", State: tc.state})

			if !strings.Contains(got, allowedMarker) {
				t.Fatalf("get_ticket on a %s ticket does not report what the state allows:\n%s", tc.state, got)
			}
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("get_ticket on a %s ticket omits the allowed %q:\n%s", tc.state, want, got)
				}
			}
			for _, refused := range tc.refused {
				if strings.Contains(got, refused) {
					t.Errorf("get_ticket on a %s ticket offers %q, which that state refuses:\n%s",
						tc.state, refused, got)
				}
			}
		})
	}
}

// TestListTickets_ReportsWhatEachColumnAllows pins the roster half. A column is
// one state, so the line is written once per column rather than per ticket —
// and an empty column, having nothing to act on, gets no line at all.
func TestListTickets_ReportsWhatEachColumnAllows(t *testing.T) {
	fb := &fakeBoard{
		getBoardFn: func(ctx context.Context) (board.Snapshot, error) {
			worker := board.WorkerID(workerW1)
			return board.Snapshot{
				Shaping: []board.Ticket{{ID: ticketT1, Title: "Shape me", State: board.StateShaping}},
				Working: []board.Ticket{
					{ID: ticketT7, Title: "Under way", State: board.StateWorking, WorkerID: &worker},
				},
				WorkerTotal: 1,
			}, nil
		},
	}
	svc := newTestService(fb, &fakeSay{}, &fakeConvo{}, &scriptedLLM{})

	res := svc.Dispatch(context.Background(), newToolCall(t, "lt", brain.ToolListTickets, brain.ListTicketsInput{}))
	if res.IsError {
		t.Fatalf("list_tickets returned error: %q", res.Content)
	}

	wantBlocks := []string{
		"## Shaping (1)\n" + allowedMarker + " on these: update_ticket title/body/priority, " +
			"update_ticket approval_requested=true, update_ticket " + argStateReady + ", " +
			toolNameDeleteTicket + "\n",
		"## Working (1)\n" + allowedMarker + " on these: " + toolNameSendToAgent + ", " +
			"update_ticket state=\"blocked\", update_ticket " + argStateDone + "\n",
	}
	for _, want := range wantBlocks {
		if !strings.Contains(res.Content, want) {
			t.Errorf("roster is missing the column block %q; got:\n%s", want, res.Content)
		}
	}
	// An empty column runs straight into the next header — no line telling the
	// model what it could do to tickets that are not there.
	if !strings.Contains(res.Content, "## Ready (0)\n## Blocked (0)\n") {
		t.Errorf("an empty column must stay a bare header; got:\n%s", res.Content)
	}
}

// TestUpdateTicket_RefusalStaysVerbatim is the constraint the preventive line
// must not disturb (06 §6): a transition the board refuses comes back exactly
// as the board worded it, with no allowed-list appended. The prompt reads that
// error as "already in that state, treat as done, never retry" — pointing at
// the alternatives here would invite precisely the retry it forbids.
func TestUpdateTicket_RefusalStaysVerbatim(t *testing.T) {
	boardErr := &board.ErrInvalidTransition{From: board.StateReady, Attempted: methodMarkReady}
	fb := &fakeBoard{
		markReadyFn: func(ctx context.Context, id board.TicketID) (board.Ticket, error) {
			return board.Ticket{}, boardErr
		},
	}
	svc := newTestService(fb, &fakeSay{}, &fakeConvo{}, &scriptedLLM{})

	call := newToolCall(t, "u1", brain.ToolUpdateTicket, brain.UpdateTicketInput{
		ID: ticketT1, State: new("ready"),
	})
	res := svc.Dispatch(context.Background(), call)

	if !res.IsError {
		t.Fatalf("update_ticket should surface the board's refusal, got: %q", res.Content)
	}
	if res.Content != boardErr.Error() {
		t.Errorf("refusal content = %q, want the board's error verbatim %q", res.Content, boardErr.Error())
	}
	if strings.Contains(res.Content, allowedMarker) {
		t.Errorf("refusal content carries an allowed-list (%q); the idempotency rule reads this error "+
			"as already-done and must not be given alternatives to retry", res.Content)
	}
}

// getTicketDetail dispatches get_ticket against a board serving exactly t.
func getTicketDetail(t *testing.T, ticket board.Ticket) string {
	t.Helper()
	fb := &fakeBoard{
		getTicketFn: func(ctx context.Context, id board.TicketID) (board.Ticket, error) {
			return ticket, nil
		},
	}
	svc := newTestService(fb, &fakeSay{}, &fakeConvo{}, &scriptedLLM{})

	res := svc.Dispatch(context.Background(), newToolCall(t, "gt", brain.ToolGetTicket, brain.GetTicketInput{
		ID: string(ticket.ID),
	}))
	if res.IsError {
		t.Fatalf("get_ticket returned error: %q", res.Content)
	}
	return res.Content
}
