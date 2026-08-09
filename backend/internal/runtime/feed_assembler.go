package runtime

import (
	"context"
	"fmt"
)

// feedPageSize is how many update/preview cards the feed snapshot carries in its
// newest page (08 D2′). History older than this pages in via FeedHistory, so a
// long-retained backlog doesn't ship in one snapshot. Also the default
// history-page size; the api clamps an explicit ?limit within [1, 100].
const feedPageSize = 30

// Feed is the feed assembler: the read-only half of the primary screen (08 §3,
// D2′). It joins current board state (blocker and proposal cards) with the
// retained notification history (update/preview/poke/done cards) into the one
// absolute FeedSnapshot the api serves and every feed SSE push carries.
//
// Read-only by construction: it holds a BoardReader and the *read* half of the
// notification store, so no method here can mutate a row or append an outbox
// entry. That is the whole point of the type — feed assembly is the one runtime
// responsibility with zero write authority, and the ports say so. Mutating the
// feed is Notifications' job; fanning the assembled snapshot out is FanOut's.
type Feed struct {
	board         BoardReader
	notifications NotificationReader
}

// NewFeed wires the assembler over its two read ports.
func NewFeed(board BoardReader, notifications NotificationReader) *Feed {
	return &Feed{board: board, notifications: notifications}
}

// Feed assembles one project's absolute feed snapshot (08 §3, D2′, 11 §3):
// board-derived blocker and proposal cards, then the newest page of
// brain-authored update/preview cards — seen AND unseen (retained history),
// newest-first — plus the header summary counts and the last-seen divider
// boundary. Backs GET /api/feed and every feed SSE push. The card order is
// strict — blockers, then proposals, then updates — because the client renders
// one ordered list and pins blockers on top.
func (f *Feed) Feed(ctx context.Context, projectID string) (FeedSnapshot, error) {
	view, err := f.board.BoardView(ctx, projectID)
	if err != nil {
		return FeedSnapshot{}, fmt.Errorf("runtime: feed board view: %w", err)
	}
	recent, hasMoreHistory, err := f.notifications.RecentNotifications(ctx, projectID, feedPageSize)
	if err != nil {
		return FeedSnapshot{}, fmt.Errorf("runtime: feed recent notifications: %w", err)
	}
	unseenCount, err := f.notifications.UnseenCount(ctx, projectID)
	if err != nil {
		return FeedSnapshot{}, fmt.Errorf("runtime: feed unseen count: %w", err)
	}
	lastSeen, err := f.notifications.LastSeenID(ctx, projectID)
	if err != nil {
		return FeedSnapshot{}, fmt.Errorf("runtime: feed last seen id: %w", err)
	}

	cards := make([]FeedCard, 0, len(view.Blocked)+len(view.Proposals)+len(recent))
	for _, t := range view.Blocked {
		id := t.ID
		cards = append(cards, FeedCard{
			Kind: "blocker", ID: "blocker:" + t.ID, Label: t.Title,
			Body: t.BlockedReason, TicketID: &id, CreatedAt: t.UpdatedAt,
		})
	}
	for _, t := range view.Proposals {
		id := t.ID
		cards = append(cards, FeedCard{
			Kind: "proposal", ID: "proposal:" + t.ID, Label: t.Title,
			Body: t.Body, TicketID: &id, CreatedAt: t.UpdatedAt,
		})
	}
	for _, n := range recent {
		if card, ok := notificationToCard(n, view.TicketTitles); ok {
			cards = append(cards, card)
		}
	}

	summary := FeedSummary{
		BlockerCount:           len(view.Blocked),
		UpdateCount:            unseenCount,
		StreamCount:            view.WorkingCount + view.BlockedCount,
		Building:               view.WorkingCount,
		Idle:                   view.BlockedCount,
		LastSeenNotificationID: lastSeen,
	}
	// RecentNotifications is newest-first, so the first row is the last word.
	if len(recent) > 0 {
		at := recent[0].CreatedAt
		summary.LastWordAt = &at
	}

	return FeedSnapshot{Summary: summary, Cards: cards, HasMoreHistory: hasMoreHistory}, nil
}

// FeedHistory assembles one older page of the project's retained
// update/preview history (08 D2′, 11 §3): notification-backed cards with
// id < before, newest-first, plus whether a further page remains.
// Board-derived blocker/proposal cards are never paged. Ticket-tagged notes
// take their label from current board titles, exactly like Feed. Backs
// GET /api/feed/history.
func (f *Feed) FeedHistory(
	ctx context.Context, projectID string, before int64, limit int,
) ([]FeedCard, bool, error) {
	view, err := f.board.BoardView(ctx, projectID)
	if err != nil {
		return nil, false, fmt.Errorf("runtime: feed history board view: %w", err)
	}
	notes, hasMore, err := f.notifications.HistoryBefore(ctx, projectID, before, limit)
	if err != nil {
		return nil, false, fmt.Errorf("runtime: feed history notifications: %w", err)
	}
	cards := make([]FeedCard, 0, len(notes))
	for _, n := range notes {
		if card, ok := notificationToCard(n, view.TicketTitles); ok {
			cards = append(cards, card)
		}
	}
	return cards, hasMore, nil
}

// notificationToCard maps a brain-authored notification row to its feed card
// (08 §3), shared by Feed's newest page and FeedHistory's older pages. A
// ticket-tagged note renders the linked ticket's current title as its label
// (titles from the board view); a note with no ticket keeps an empty label,
// which the client renders headless-but-legible.
//
// Returns ok=false when the note is tagged to a ticket that is no longer on the
// board — i.e. the ticket has been archived (deleted). Its title is absent from
// the board view, so the card would otherwise render title-less (a persistent
// "done" card as a bare ✅, or an update/preview as a headless row). Instead the
// card vanishes from the feed entirely, mirroring how board-derived
// blocker/proposal cards disappear when GetBoard stops returning their ticket
// (03 §4 — an archived ticket disappears from every read). The comma-ok lookup
// distinguishes an archived ticket (absent) from a live one whose title is
// present, so untagged notes (TicketID nil) still render headless as before.
func notificationToCard(n Notification, titles map[string]string) (FeedCard, bool) {
	nid := n.ID
	card := FeedCard{
		Kind: string(n.Kind), ID: fmt.Sprintf("update:%d", n.ID),
		Body: n.Body, TicketID: n.TicketID, NotificationID: &nid,
		CreatedAt: n.CreatedAt, SeenAt: n.SeenAt,
	}
	if n.TicketID != nil {
		title, live := titles[*n.TicketID]
		if !live {
			return FeedCard{}, false
		}
		card.Label = title
	}
	if n.Kind == KindPreview {
		card.ImageURL = n.ImageURL
	}
	if n.Kind == KindDone {
		card.GitHubURL = n.GitHubURL
		card.GitHubLabel = n.GitHubLabel
		card.WorkSummary = n.WorkSummary
	}
	return card, true
}
