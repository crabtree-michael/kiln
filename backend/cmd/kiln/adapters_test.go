package main

import (
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/board"
)

// TestNotifyURL pins the tap-to-open deep link (02 §10): a ticket-bearing
// notify.send lands on `/?ticket=<id>` so the frontend opens that proposal, and
// a ticketless payload falls back to the plain board root.
func TestNotifyURL(t *testing.T) {
	cases := []struct {
		name string
		id   board.TicketID
		want string
	}{
		{name: "ticket", id: "t-123", want: "/?ticket=t-123"},
		{name: "empty falls back to root", id: "", want: "/"},
		{name: "id needing escaping", id: "a b/c", want: "/?ticket=a+b%2Fc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := notifyURL(tc.id); got != tc.want {
				t.Fatalf("notifyURL(%q) = %q, want %q", tc.id, got, tc.want)
			}
		})
	}
}
