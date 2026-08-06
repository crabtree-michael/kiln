package brain_test

// The two halves of "the roster stays lean, history stays reachable": the Done
// column is windowed to its most recent few in list_tickets, and search_tickets
// is the paged keyword lookup that reaches everything the window drops.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/board"
	"github.com/crabtree-michael/kiln/backend/internal/brain"
)

const (
	toolNameSearchTickets = "search_tickets"
	queryPayments         = "payments"
	doneD1                = "d-1"
	doneD2                = "d-2"
)

// TestListTickets_DoneColumnIsCappedAtFive pins the roster window: however long
// Done grows, the model sees its five most recent (Snapshot.Done is newest-first)
// — and is told, in the same breath, both how many the column really holds and
// that search_tickets reaches the rest. A windowed column that read like a
// complete one would be worse than a long one.
func TestListTickets_DoneColumnIsCappedAtFive(t *testing.T) {
	done := make([]board.Ticket, 0, 9)
	for i := 1; i <= 9; i++ {
		done = append(done, board.Ticket{
			ID:    board.TicketID(fmt.Sprintf("d-%d", i)),
			Title: fmt.Sprintf("Landed %d", i),
			State: board.StateDone,
		})
	}
	content := roster(t, board.Snapshot{
		Shaping: []board.Ticket{{ID: ticketT1, Title: "Shape me", State: board.StateShaping}},
		Done:    done,
	})

	if !strings.Contains(content, "## Done (9)\n") {
		t.Errorf("the header must count the whole column, not the window; got:\n%s", content)
	}
	for i := 1; i <= 5; i++ {
		if !strings.Contains(content, fmt.Sprintf("[d-%d]", i)) {
			t.Errorf("the %d most recent done tickets must be listed; d-%d is missing:\n%s", 5, i, content)
		}
	}
	for i := 6; i <= 9; i++ {
		if strings.Contains(content, fmt.Sprintf("[d-%d]", i)) {
			t.Errorf("done ticket d-%d is past the cap and must not be listed:\n%s", i, content)
		}
	}
	if !strings.Contains(content, "(4 older not shown — find them with "+toolNameSearchTickets+")") {
		t.Errorf("the elided remainder must be named, with the way to reach it; got:\n%s", content)
	}
	// The live columns are never windowed — the brain acts on all of them.
	if strings.Contains(content, "## Shaping (1)\n"+allowedMarker+" on these") &&
		!strings.Contains(content, "[t-1]") {
		t.Errorf("live columns must still list every ticket; got:\n%s", content)
	}
}

// TestListTickets_ShortDoneColumnIsWhole is the cap's other edge: a Done column
// at or under the limit is complete, and must not carry an elision line
// suggesting there is more behind it.
func TestListTickets_ShortDoneColumnIsWhole(t *testing.T) {
	content := roster(t, board.Snapshot{
		Done: []board.Ticket{
			{ID: doneD1, Title: "Landed 1", State: board.StateDone},
			{ID: doneD2, Title: "Landed 2", State: board.StateDone},
		},
	})

	if !strings.Contains(content, "[d-1]") || !strings.Contains(content, "[d-2]") {
		t.Errorf("a short done column must be listed whole; got:\n%s", content)
	}
	if strings.Contains(content, "not shown") {
		t.Errorf("a complete column must not claim an elided tail; got:\n%s", content)
	}
}

// TestSearchTickets_MatchesAcrossTheWholeBoard pins what a query reaches: every
// column (Done included, which is the point), on id, title and body, folding
// case and matching inside a word.
func TestSearchTickets_MatchesAcrossTheWholeBoard(t *testing.T) {
	snap := board.Snapshot{
		Shaping: []board.Ticket{{ID: ticketT1, Title: "Refund flow", Body: "Touches PAYMENTS.", State: board.StateShaping}},
		Working: []board.Ticket{{ID: ticketT7, Title: "Unrelated", Body: "nothing here", State: board.StateWorking}},
		Done: []board.Ticket{
			{ID: doneD1, Title: "Stripe payments webhook", State: board.StateDone},
			{ID: doneD2, Title: "Old login fix", State: board.StateDone},
		},
	}
	content := search(t, snap, brain.SearchTicketsInput{Query: "Payment"})

	for _, want := range []string{"[d-1]", "[t-1]"} {
		if !strings.Contains(content, want) {
			t.Errorf("search must match %s (case-insensitive, inside a word, any column); got:\n%s", want, content)
		}
	}
	for _, notWant := range []string{"[t-7]", "[d-2]"} {
		if strings.Contains(content, notWant) {
			t.Errorf("search returned non-matching ticket %s:\n%s", notWant, content)
		}
	}
	// A title match ranks above a body-only match — the ticket whose title
	// carries the words is the one that was most likely meant.
	if strings.Index(content, "[d-1]") > strings.Index(content, "[t-1]") {
		t.Errorf("a title match must rank above a body-only match; got:\n%s", content)
	}
	// The body-only match shows why it matched, so the model need not spend a
	// get_ticket to find out.
	if !strings.Contains(content, "Touches PAYMENTS.") {
		t.Errorf("a body-only match must carry an excerpt of the matching body; got:\n%s", content)
	}
}

// TestSearchTickets_EveryWordMustAppear pins AND semantics: adding a word always
// narrows, which is the only way the model can steer a query it cannot see the
// index for.
func TestSearchTickets_EveryWordMustAppear(t *testing.T) {
	snap := board.Snapshot{Done: []board.Ticket{
		{ID: doneD1, Title: "Stripe payments webhook", State: board.StateDone},
		{ID: doneD2, Title: "Payments dashboard", State: board.StateDone},
	}}

	both := search(t, snap, brain.SearchTicketsInput{Query: "stripe payments"})
	if !strings.Contains(both, "[d-1]") {
		t.Errorf("the ticket carrying both words must match; got:\n%s", both)
	}
	if strings.Contains(both, "[d-2]") {
		t.Errorf("a ticket missing one of the words must not match; got:\n%s", both)
	}
}

// TestSearchTickets_PagesThroughMatches pins the pagination contract: a bounded
// page, a header saying where it sits in the whole result set, and — while there
// is more — the exact next call spelled out, so paging never rests on the model
// doing arithmetic.
func TestSearchTickets_PagesThroughMatches(t *testing.T) {
	done := make([]board.Ticket, 0, 12)
	base := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	for i := 1; i <= 12; i++ {
		done = append(done, board.Ticket{
			ID:    board.TicketID(fmt.Sprintf("d-%02d", i)),
			Title: fmt.Sprintf("Payments change %d", i),
			State: board.StateDone,
			// Descending recency, so the page order is the listed id order.
			UpdatedAt: base.Add(-time.Duration(i) * time.Hour),
		})
	}
	snap := board.Snapshot{Done: done}

	first := search(t, snap, brain.SearchTicketsInput{Query: queryPayments})
	if !strings.Contains(first, "12 matching tickets") || !strings.Contains(first, "page 1 of 3") {
		t.Errorf("a page must say where it sits in the whole result set; got:\n%s", first)
	}
	if n := strings.Count(first, "\n- ["); n != 5 {
		t.Errorf("a page carries 5 results, got %d:\n%s", n, first)
	}
	if !strings.Contains(first, `more: `+toolNameSearchTickets+` query="`+queryPayments+`" page=2`) {
		t.Errorf("a page with more behind it must spell out the next call; got:\n%s", first)
	}

	second := search(t, snap, brain.SearchTicketsInput{Query: queryPayments, Page: 2})
	if !strings.Contains(second, "[d-06]") || strings.Contains(second, "[d-01]") {
		t.Errorf("page 2 must continue where page 1 stopped; got:\n%s", second)
	}

	last := search(t, snap, brain.SearchTicketsInput{Query: queryPayments, Page: 3})
	if strings.Contains(last, "more: ") {
		t.Errorf("the last page must not offer a next one; got:\n%s", last)
	}
}

// TestSearchTickets_EmptyOutcomesAreStated pins the two "nothing here" answers.
// Both are ordinary results, not errors: they are the true answer to a
// well-formed question, and an error would invite a retry.
func TestSearchTickets_EmptyOutcomesAreStated(t *testing.T) {
	snap := board.Snapshot{Done: []board.Ticket{{ID: doneD1, Title: "Login fix", State: board.StateDone}}}

	none := search(t, snap, brain.SearchTicketsInput{Query: "quicksort"})
	if !strings.Contains(none, "no tickets match") {
		t.Errorf("a query with no matches must say so; got:\n%s", none)
	}

	past := search(t, snap, brain.SearchTicketsInput{Query: "login", Page: 9})
	if !strings.Contains(past, "no page 9") || !strings.Contains(past, "1 page in all") {
		t.Errorf("a page past the end must say how many pages there are; got:\n%s", past)
	}
}

// TestSearchTickets_BlankQueryIsMalformed pins the one argument-shape rule
// (06 §8): a blank query has no answer — reported as malformed rather than
// silently reading as "no tickets", which would look like an empty board.
func TestSearchTickets_BlankQueryIsMalformed(t *testing.T) {
	fb := &fakeBoard{}
	svc := newTestService(fb, &fakeSay{}, &fakeConvo{}, &scriptedLLM{})

	res := svc.Dispatch(context.Background(),
		newToolCall(t, "s1", brain.ToolSearchTickets, brain.SearchTicketsInput{Query: "   "}))

	if !res.IsError || !strings.Contains(res.Content, toolNameSearchTickets) {
		t.Errorf("a blank query must come back as a malformed call naming the tool; got %q", res.Content)
	}
	if fb.getBoardCount() != 0 {
		t.Errorf("a malformed search must not read the board, got %d reads", fb.getBoardCount())
	}
}

// TestSearchTickets_BoardErrorComesBackVerbatim keeps search under the same rule
// as every other tool (06 §6, §8): a port failure is a tool result, not a pass
// failure, and carries the error's own words.
func TestSearchTickets_BoardErrorComesBackVerbatim(t *testing.T) {
	fb := &fakeBoard{
		getBoardFn: func(ctx context.Context) (board.Snapshot, error) {
			return board.Snapshot{}, board.ErrNotFound
		},
	}
	svc := newTestService(fb, &fakeSay{}, &fakeConvo{}, &scriptedLLM{})

	res := svc.Dispatch(context.Background(),
		newToolCall(t, "s2", brain.ToolSearchTickets, brain.SearchTicketsInput{Query: queryPayments}))

	if !res.IsError || res.Content != board.ErrNotFound.Error() {
		t.Errorf("search error = %q (is_error=%t), want the port error verbatim", res.Content, res.IsError)
	}
}

// roster dispatches list_tickets against a board serving snap.
func roster(t *testing.T, snap board.Snapshot) string {
	t.Helper()
	return dispatchRead(t, snap, "lt", brain.ToolListTickets, brain.ListTicketsInput{})
}

// search dispatches search_tickets against a board serving snap.
func search(t *testing.T, snap board.Snapshot, in brain.SearchTicketsInput) string {
	t.Helper()
	return dispatchRead(t, snap, "st", brain.ToolSearchTickets, in)
}

// dispatchRead runs one read tool against a board serving snap and returns its
// content, failing the test if the tool errored.
func dispatchRead(t *testing.T, snap board.Snapshot, callID string, name brain.ToolName, in any) string {
	t.Helper()
	fb := &fakeBoard{
		getBoardFn: func(ctx context.Context) (board.Snapshot, error) { return snap, nil },
	}
	svc := newTestService(fb, &fakeSay{}, &fakeConvo{}, &scriptedLLM{})

	res := svc.Dispatch(context.Background(), newToolCall(t, callID, name, in))
	if res.IsError {
		t.Fatalf("%s returned error: %q", name, res.Content)
	}
	return res.Content
}
