package brain

import (
	"fmt"
	"sort"
	"strings"

	"github.com/crabtree-michael/kiln/backend/internal/board"
)

// search_tickets' shape (06 §4). The roster (list_tickets) shows the live board
// and only the newest doneRosterLimit Done tickets; search is how the brain
// reaches everything else — an older accepted ticket, a duplicate it half
// remembers — without ever pulling the whole history into a pass.
//
// Deliberately simple: a substring keyword match over the snapshot the board
// already serves, not a stored index. A project's board is small enough that
// filtering it in memory costs less than a second read path would, and
// "does this word appear in this ticket" is the whole question the brain asks.
const (
	// searchPageSize bounds one page of results. Small on purpose: a page is
	// context spent, and a query that returns more than a handful of tickets is
	// usually a query worth narrowing rather than paging through.
	searchPageSize = 5
	// searchExcerptBytes bounds the body excerpt carried under a body-only match,
	// so the model can see *why* a ticket matched without paying for its body.
	searchExcerptBytes = 160
)

// searchHit is one matching ticket plus what the match looked like: whether the
// title carried it (the stronger signal, ranked first) and, when the body did,
// a short excerpt around the first matched word.
type searchHit struct {
	ticket   board.Ticket
	titleHit bool
	excerpt  string
}

// searchTickets returns the snapshot's tickets matching every word in words,
// best first. Matching is case-insensitive substring over id, title, body and
// blocked reason — "auth" finds "authentication" — and multiple words are ANDed,
// so adding a word always narrows.
//
// Ranking is title matches first, then most recently updated, then by id: the
// ticket whose *title* carries the words is the one the user most likely meant,
// and recency breaks the rest. Fully deterministic, so a page-2 call sees the
// same order page 1 did (no cursor to carry — the board is not mutating
// mid-pass, 06 §5).
func searchTickets(snap board.Snapshot, words []string) []searchHit {
	var hits []searchHit
	for _, t := range allTickets(snap) {
		if !matchesAll(ticketHaystack(t), words) {
			continue
		}
		hit := searchHit{ticket: t, titleHit: matchesAll(strings.ToLower(t.Title), words)}
		if !hit.titleHit {
			hit.excerpt = bodyExcerpt(t.Body, words)
		}
		hits = append(hits, hit)
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].titleHit != hits[j].titleHit {
			return hits[i].titleHit
		}
		if !hits[i].ticket.UpdatedAt.Equal(hits[j].ticket.UpdatedAt) {
			return hits[i].ticket.UpdatedAt.After(hits[j].ticket.UpdatedAt)
		}
		return hits[i].ticket.ID < hits[j].ticket.ID
	})
	return hits
}

// allTickets flattens a snapshot's five columns into one list. Search is
// state-blind — the point is to find a ticket wherever it sits, most often in
// the Done history the roster no longer prints in full.
func allTickets(snap board.Snapshot) []board.Ticket {
	all := make([]board.Ticket, 0,
		len(snap.Shaping)+len(snap.Ready)+len(snap.Blocked)+len(snap.Working)+len(snap.Done))
	all = append(all, snap.Shaping...)
	all = append(all, snap.Ready...)
	all = append(all, snap.Blocked...)
	all = append(all, snap.Working...)
	all = append(all, snap.Done...)
	return all
}

// ticketHaystack is the lowercased text one ticket is searched over: its id
// (so a half-remembered id finds it), title, body and blocked reason.
func ticketHaystack(t board.Ticket) string {
	var b strings.Builder
	b.WriteString(string(t.ID))
	b.WriteByte('\n')
	b.WriteString(t.Title)
	b.WriteByte('\n')
	b.WriteString(t.Body)
	if t.BlockedReason != nil {
		b.WriteByte('\n')
		b.WriteString(*t.BlockedReason)
	}
	return strings.ToLower(b.String())
}

// matchesAll reports whether every word appears in the (already lowercased)
// haystack. An empty word list matches nothing — the caller rejects a blank
// query before this, so it never means "match everything".
func matchesAll(haystack string, words []string) bool {
	if len(words) == 0 {
		return false
	}
	for _, w := range words {
		if !strings.Contains(haystack, w) {
			return false
		}
	}
	return true
}

// searchWords splits a query into the lowercased words that must all appear.
// Whitespace-separated and nothing cleverer: no stemming, no quoting, no
// operators — the model writes the query, and a rule it can't see is a rule it
// can't use.
func searchWords(query string) []string {
	return strings.Fields(strings.ToLower(query))
}

// bodyExcerpt returns a single-line window of body around the first occurrence
// of any word, bounded to searchExcerptBytes. Empty when no word is in the body
// (the title carried the match instead). Byte-sliced with rune-boundary trims,
// so a multibyte character at an edge is dropped rather than cut in half.
func bodyExcerpt(body string, words []string) string {
	flat := strings.Join(strings.Fields(body), " ")
	at := firstIndex(strings.ToLower(flat), words)
	if at < 0 {
		return ""
	}
	start := max(at-searchExcerptBytes/4, 0)
	end := min(start+searchExcerptBytes, len(flat))
	excerpt := strings.ToValidUTF8(flat[start:end], "")
	if start > 0 {
		excerpt = "…" + excerpt
	}
	if end < len(flat) {
		excerpt += "…"
	}
	return excerpt
}

// firstIndex is the earliest position in haystack at which any word occurs, or
// -1 when none does.
func firstIndex(haystack string, words []string) int {
	first := -1
	for _, w := range words {
		if at := strings.Index(haystack, w); at >= 0 && (first < 0 || at < first) {
			first = at
		}
	}
	return first
}

// formatSearchResults renders one page of hits — the compact roster line per
// ticket, plus the excerpt when the body is what matched. Every outcome states
// where the page sits in the whole result set, and a page with more behind it
// spells out the exact next call, so paging never needs the model to work out
// the arithmetic. page is 1-based and already clamped to >= 1 by the caller.
func formatSearchResults(query string, hits []searchHit, page int) string {
	if len(hits) == 0 {
		return fmt.Sprintf("no tickets match %q", query)
	}
	pages := (len(hits) + searchPageSize - 1) / searchPageSize
	if page > pages {
		return fmt.Sprintf("no page %d — %s for %q, %s in all",
			page, plural(len(hits), "matching ticket"), query, plural(pages, "page"))
	}
	start := (page - 1) * searchPageSize
	end := min(start+searchPageSize, len(hits))

	var b strings.Builder
	fmt.Fprintf(&b, "%s for %q — page %d of %d, showing %d-%d\n",
		plural(len(hits), "matching ticket"), query, page, pages, start+1, end)
	for _, h := range hits[start:end] {
		t := h.ticket
		fmt.Fprintf(&b, "- [%s] %q (state=%s, priority=%d)\n", t.ID, t.Title, t.State, t.Priority)
		if h.excerpt != "" {
			fmt.Fprintf(&b, "  %s\n", h.excerpt)
		}
	}
	if page < pages {
		fmt.Fprintf(&b, "more: %s query=%q page=%d\n", ToolSearchTickets, query, page+1)
	}
	return b.String()
}

// plural counts noun, adding a plain "s" for anything but one. Every count in a
// tool result is read by a model that takes the wording at face value, so
// "1 matching tickets" is worth the three lines it costs to avoid.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
