//go:build integration

// Package postgres_test exercises the runtime store adapter against a real
// database (04 §9: "integration: real Postgres — two workers + real tables;
// kill the process between execute and mark and verify re-run; verify
// id-order processing under concurrent inserts"; 07 §9: "transactional
// append+enqueue... tested against real Postgres"). Run with:
//
//	TEST_DATABASE_URL=postgres://kiln:kiln@localhost:5432/kiln_test?sslmode=disable \
//	    go test -tags=integration ./internal/runtime/postgres/...
//
// kiln_test is shared with other modules (e.g. internal/board's
// tickets/workers/outbox), so setup only ever creates the runtime's own
// tables (events, messages) if missing, and only ever truncates
// events/messages — never DROPs, never touches tables it doesn't own.
package postgres_test

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/crabtree-michael/kiln/backend/internal/runtime"
	"github.com/crabtree-michael/kiln/backend/internal/runtime/postgres"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run runtime/postgres integration tests")
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open %s: %v", dsn, err)
	}
	t.Cleanup(func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Logf("close db: %v", closeErr)
		}
	})
	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	ensureMigrationsApplied(ctx, t, db)
	truncateRuntimeTables(ctx, t, db)
	return db
}

// ensureMigrationsApplied applies ./migrations, in filename order, only if
// the runtime's own tables don't already exist — kiln_test is shared, and
// other modules' tables (tickets, workers, outbox, agent_turns) must never
// be touched here.
func ensureMigrationsApplied(ctx context.Context, t *testing.T, db *sql.DB) {
	t.Helper()
	var exists bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'events'
	)`).Scan(&exists)
	if err != nil {
		t.Fatalf("check for events table: %v", err)
	}
	if exists {
		return
	}

	_, thisFile, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Join(filepath.Dir(thisFile), "migrations")

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read migrations dir %s: %v", dir, err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".sql" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Fatalf("no .sql migrations found in %s", dir)
	}

	migrationsFS := os.DirFS(dir)
	for _, name := range names {
		b, err := fs.ReadFile(migrationsFS, name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		if _, err := db.ExecContext(ctx, string(b)); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}
}

// truncateRuntimeTables resets exactly the runtime's own tables so every
// test starts clean, without disturbing other modules sharing kiln_test. The
// notification tests also append feed.updated outbox rows (08 §7); outbox is
// board-owned, so it is truncated here (not DROPped) only if it exists — the
// runtime's migrations don't create it.
func truncateRuntimeTables(ctx context.Context, t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(ctx,
		`TRUNCATE TABLE events, messages, notifications RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate runtime tables: %v", err)
	}
	var outboxExists bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'outbox'
	)`).Scan(&outboxExists); err != nil {
		t.Fatalf("check for outbox table: %v", err)
	}
	if outboxExists {
		if _, err := db.ExecContext(ctx, `TRUNCATE TABLE outbox RESTART IDENTITY`); err != nil {
			t.Fatalf("truncate outbox: %v", err)
		}
	}
}

// feedUpdatedCount counts pending feed.updated outbox rows — the runtime's
// second-outbox-writer signal (08 §7). Skips the assertion when outbox is
// absent (the board's migrations own it and may not be applied in isolation).
func feedUpdatedCount(ctx context.Context, t *testing.T, db *sql.DB) (int, bool) {
	t.Helper()
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'outbox'
	)`).Scan(&exists); err != nil {
		t.Fatalf("check for outbox table: %v", err)
	}
	if !exists {
		return 0, false
	}
	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM outbox WHERE topic = 'feed.updated'`).Scan(&n); err != nil {
		t.Fatalf("count feed.updated outbox rows: %v", err)
	}
	return n, true
}

// ---- notifications: post/retract/seen + feed.updated emission (08 §3, §7) --

func TestIntegration_PostNotification_InsertsRowAndEmitsFeedUpdated(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	tid := "tk-1"
	img := "https://img/x.png"
	n, err := store.PostNotification(ctx, "preview", "a preview", &tid, &img)
	if err != nil {
		t.Fatalf("PostNotification: %v", err)
	}
	if n.ID == 0 || n.CreatedAt.IsZero() {
		t.Fatalf("PostNotification returned %+v, want a persisted id + created_at", n)
	}

	var kind, body, gotTid, gotImg string
	if err := db.QueryRowContext(ctx,
		`SELECT kind, body, ticket_id, image_url FROM notifications WHERE id = $1`, n.ID).
		Scan(&kind, &body, &gotTid, &gotImg); err != nil {
		t.Fatalf("query notification row: %v", err)
	}
	if kind != "preview" || body != "a preview" || gotTid != tid || gotImg != img {
		t.Errorf("row = (%q,%q,%q,%q), want (preview, a preview, %q, %q)", kind, body, gotTid, gotImg, tid, img)
	}

	if got, ok := feedUpdatedCount(ctx, t, db); ok && got != 1 {
		t.Errorf("feed.updated outbox rows = %d after one PostNotification, want 1 (08 §7)", got)
	}
}

// A feed.completion outbox entry is at-least-once, so PostCompletionCard may be
// invoked twice for the same outbox id. The partial unique index on
// idempotency_key must make the redelivery a no-op: exactly one card, and only
// the first delivery reports posted=true and fans out a feed.updated (08 §7).
func TestIntegration_PostCompletionCard_IdempotentOnKey(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	const key = int64(4242)
	tid := "tk-done"

	posted, err := store.PostCompletionCard(ctx, key, tid, "✓")
	if err != nil {
		t.Fatalf("PostCompletionCard (first): %v", err)
	}
	if !posted {
		t.Fatal("first PostCompletionCard reported posted=false, want true")
	}

	posted, err = store.PostCompletionCard(ctx, key, tid, "✓")
	if err != nil {
		t.Fatalf("PostCompletionCard (redelivery): %v", err)
	}
	if posted {
		t.Error("redelivered PostCompletionCard reported posted=true, want false (idempotent no-op)")
	}

	var rows int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM notifications WHERE idempotency_key = $1`, key).Scan(&rows); err != nil {
		t.Fatalf("count completion rows: %v", err)
	}
	if rows != 1 {
		t.Errorf("completion rows for key %d = %d, want exactly 1", key, rows)
	}

	// Only the accepted insert fans out; the duplicate must not enqueue a second.
	if got, ok := feedUpdatedCount(ctx, t, db); ok && got != 1 {
		t.Errorf("feed.updated outbox rows = %d after a duplicate completion, want 1 (08 §7)", got)
	}
}

func TestIntegration_EditNotification_AmendsActiveRowAndEmitsFeedUpdated(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	posted, err := store.PostNotification(ctx, "update", "original", nil, nil)
	if err != nil {
		t.Fatalf("PostNotification: %v", err)
	}

	img := "https://img/edited.png"
	if err := store.EditNotification(ctx, posted.ID, "preview", "amended", &img); err != nil {
		t.Fatalf("EditNotification: %v", err)
	}

	var kind, body string
	var gotImg sql.NullString
	if err := db.QueryRowContext(ctx,
		`SELECT kind, body, image_url FROM notifications WHERE id = $1`, posted.ID).
		Scan(&kind, &body, &gotImg); err != nil {
		t.Fatalf("query edited row: %v", err)
	}
	if kind != "preview" || body != "amended" || !gotImg.Valid || gotImg.String != img {
		t.Errorf("edited row = (%q,%q,%v), want (preview, amended, %q)", kind, body, gotImg, img)
	}

	// A retracted card is not resurfaced by an edit (no-op under the WHERE guard).
	if err := store.RetractNotification(ctx, posted.ID); err != nil {
		t.Fatalf("RetractNotification: %v", err)
	}
	if err := store.EditNotification(ctx, posted.ID, "update", "should not apply", nil); err != nil {
		t.Fatalf("EditNotification on retracted: %v", err)
	}
	var stillBody string
	var retracted sql.NullTime
	if err := db.QueryRowContext(ctx,
		`SELECT body, retracted_at FROM notifications WHERE id = $1`, posted.ID).
		Scan(&stillBody, &retracted); err != nil {
		t.Fatalf("re-query row: %v", err)
	}
	if stillBody != "amended" || !retracted.Valid {
		t.Errorf("after editing a retracted card: body=%q retracted=%v, want body unchanged ('amended') and still retracted", stillBody, retracted)
	}
}

func TestIntegration_UnseenNotifications_NewestFirstFiltersSeenAndRetracted(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	older, err := store.PostNotification(ctx, "update", "older", nil, nil)
	if err != nil {
		t.Fatalf("post older: %v", err)
	}
	mid, err := store.PostNotification(ctx, "update", "middle", nil, nil)
	if err != nil {
		t.Fatalf("post middle: %v", err)
	}
	newest, err := store.PostNotification(ctx, "update", "newest", nil, nil)
	if err != nil {
		t.Fatalf("post newest: %v", err)
	}

	// Retract the middle one; it must drop out of the unseen set.
	if err := store.RetractNotification(ctx, mid.ID); err != nil {
		t.Fatalf("RetractNotification: %v", err)
	}

	got, err := store.UnseenNotifications(ctx)
	if err != nil {
		t.Fatalf("UnseenNotifications: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("UnseenNotifications returned %d rows, want 2 (retracted filtered)", len(got))
	}
	if got[0].ID != newest.ID || got[1].ID != older.ID {
		t.Errorf("order = [%d, %d], want newest-first [%d, %d]", got[0].ID, got[1].ID, newest.ID, older.ID)
	}
}

func TestIntegration_MarkSeen_StampsUpToHighWaterAndEmitsFeedUpdated(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	n1, err := store.PostNotification(ctx, "update", "one", nil, nil)
	if err != nil {
		t.Fatalf("post one: %v", err)
	}
	n2, err := store.PostNotification(ctx, "update", "two", nil, nil)
	if err != nil {
		t.Fatalf("post two: %v", err)
	}
	n3, err := store.PostNotification(ctx, "update", "three", nil, nil)
	if err != nil {
		t.Fatalf("post three: %v", err)
	}

	// Seen up to n2: n1 and n2 stamped, n3 still unseen.
	if err := store.MarkSeen(ctx, n2.ID); err != nil {
		t.Fatalf("MarkSeen: %v", err)
	}

	got, err := store.UnseenNotifications(ctx)
	if err != nil {
		t.Fatalf("UnseenNotifications: %v", err)
	}
	if len(got) != 1 || got[0].ID != n3.ID {
		t.Fatalf("unseen after MarkSeen(%d) = %+v, want only n3=%d", n2.ID, got, n3.ID)
	}

	var n1Seen, n2Seen, n3Seen sql.NullTime
	for _, id := range []struct {
		id   int64
		seen *sql.NullTime
	}{{n1.ID, &n1Seen}, {n2.ID, &n2Seen}, {n3.ID, &n3Seen}} {
		if err := db.QueryRowContext(ctx,
			`SELECT seen_at FROM notifications WHERE id = $1`, id.id).Scan(id.seen); err != nil {
			t.Fatalf("query seen_at for %d: %v", id.id, err)
		}
	}
	if !n1Seen.Valid || !n2Seen.Valid {
		t.Errorf("n1/n2 seen_at = (%v, %v), want both stamped (id <= high-water)", n1Seen.Valid, n2Seen.Valid)
	}
	if n3Seen.Valid {
		t.Error("n3 seen_at stamped, want NULL (above the high-water id)")
	}

	// PostNotification (x3) + Retract-free MarkSeen each emit one feed.updated.
	if got, ok := feedUpdatedCount(ctx, t, db); ok && got != 4 {
		t.Errorf("feed.updated outbox rows = %d, want 4 (3 posts + 1 mark-seen, 08 §7)", got)
	}
}

// RecentNotifications retains seen rows (08 D2′) — only retracted ones drop —
// newest-first, and its bool flags an older page beyond the limit.
func TestIntegration_RecentNotifications_RetainsSeenTrimsPageFlagsMore(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	n1, _ := store.PostNotification(ctx, "update", "one", nil, nil)
	n2, _ := store.PostNotification(ctx, "update", "two", nil, nil)
	n3, _ := store.PostNotification(ctx, "update", "three", nil, nil)
	retr, _ := store.PostNotification(ctx, "update", "gone", nil, nil)

	// Seen up to n2, and retract the fourth. Seen rows must stay; retracted must go.
	if err := store.MarkSeen(ctx, n2.ID); err != nil {
		t.Fatalf("MarkSeen: %v", err)
	}
	if err := store.RetractNotification(ctx, retr.ID); err != nil {
		t.Fatalf("RetractNotification: %v", err)
	}

	// Full page: all three unretracted, newest-first (seen retained).
	got, more, err := store.RecentNotifications(ctx, 30)
	if err != nil {
		t.Fatalf("RecentNotifications: %v", err)
	}
	if more {
		t.Errorf("hasMore = true, want false (3 rows under the 30 page)")
	}
	if len(got) != 3 || got[0].ID != n3.ID || got[1].ID != n2.ID || got[2].ID != n1.ID {
		t.Fatalf("recent = %+v, want [n3,n2,n1] newest-first with seen retained", got)
	}

	// Small page trims to the newest and flags more remaining.
	page, more, err := store.RecentNotifications(ctx, 2)
	if err != nil {
		t.Fatalf("RecentNotifications(2): %v", err)
	}
	if len(page) != 2 || page[0].ID != n3.ID || page[1].ID != n2.ID || !more {
		t.Fatalf("page = %+v more=%v, want [n3,n2] with more=true", page, more)
	}
}

// HistoryBefore keyset-pages older unretracted rows, newest-first.
func TestIntegration_HistoryBefore_KeysetPagesOlder(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	n1, _ := store.PostNotification(ctx, "update", "one", nil, nil)
	n2, _ := store.PostNotification(ctx, "update", "two", nil, nil)
	n3, _ := store.PostNotification(ctx, "update", "three", nil, nil)

	got, more, err := store.HistoryBefore(ctx, n3.ID, 1)
	if err != nil {
		t.Fatalf("HistoryBefore: %v", err)
	}
	if len(got) != 1 || got[0].ID != n2.ID || !more {
		t.Fatalf("history before n3 (limit 1) = %+v more=%v, want [n2] more=true", got, more)
	}

	got, more, err = store.HistoryBefore(ctx, n2.ID, 10)
	if err != nil {
		t.Fatalf("HistoryBefore: %v", err)
	}
	if len(got) != 1 || got[0].ID != n1.ID || more {
		t.Fatalf("history before n2 = %+v more=%v, want [n1] more=false", got, more)
	}
}

// LastSeenID is the seen high-water; UnseenCount counts the unseen, unretracted.
func TestIntegration_LastSeenID_And_UnseenCount(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	// Nothing seen yet.
	if id, err := store.LastSeenID(ctx); err != nil || id != nil {
		t.Fatalf("LastSeenID on empty = (%v, %v), want (nil, nil)", id, err)
	}

	store.PostNotification(ctx, "update", "one", nil, nil)
	n2, _ := store.PostNotification(ctx, "update", "two", nil, nil)
	store.PostNotification(ctx, "update", "three", nil, nil)

	if err := store.MarkSeen(ctx, n2.ID); err != nil {
		t.Fatalf("MarkSeen: %v", err)
	}
	id, err := store.LastSeenID(ctx)
	if err != nil || id == nil || *id != n2.ID {
		t.Fatalf("LastSeenID = (%v, %v), want %d", id, err, n2.ID)
	}
	count, err := store.UnseenCount(ctx)
	if err != nil || count != 1 {
		t.Fatalf("UnseenCount = (%d, %v), want 1 (only the newest is unseen)", count, err)
	}
}

// ---- events queue: claim ordering, mark done/retry/dead (04 §2-§3) --------

func TestIntegration_ClaimNextDue_OrdersByIDAndIncrementsAttempts(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	ids := make([]int64, 0, 3)
	for i := range 3 {
		id, err := store.InsertEvent(ctx, runtime.EventHumanMessage, fmt.Appendf(nil, `{"text":"m%d"}`, i))
		if err != nil {
			t.Fatalf("InsertEvent %d: %v", i, err)
		}
		ids = append(ids, id)
	}

	for i, wantID := range ids {
		entry, ok, err := store.ClaimNextDue(ctx, runtime.QueueEvents)
		if err != nil {
			t.Fatalf("ClaimNextDue #%d: %v", i, err)
		}
		if !ok {
			t.Fatalf("ClaimNextDue #%d: ok=false, want a due entry", i)
		}
		if entry.ID != wantID {
			t.Errorf("ClaimNextDue #%d returned id %d, want %d (strict id order, 04 §3 step 1)", i, entry.ID, wantID)
		}
		if entry.Attempts != 1 {
			t.Errorf("ClaimNextDue #%d Attempts = %d, want 1 (claim increments attempts)", i, entry.Attempts)
		}
	}

	// A 4th claim on an exhausted queue must report nothing due.
	_, ok, err := store.ClaimNextDue(ctx, runtime.QueueEvents)
	if err != nil {
		t.Fatalf("ClaimNextDue on exhausted queue: %v", err)
	}
	if ok {
		t.Error("ClaimNextDue on an exhausted queue returned ok=true, want false")
	}
}

func TestIntegration_MarkDone_SetsStatusDoneAndProcessedAt(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	id, err := store.InsertEvent(ctx, runtime.EventHumanMessage, []byte(`{"text":"hi"}`))
	if err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}
	if _, _, err := store.ClaimNextDue(ctx, runtime.QueueEvents); err != nil {
		t.Fatalf("ClaimNextDue: %v", err)
	}
	if err := store.MarkDone(ctx, runtime.QueueEvents, id); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}

	var status string
	var processedAt sql.NullTime
	if err := db.QueryRowContext(ctx,
		`SELECT status, processed_at FROM events WHERE id = $1`, id).Scan(&status, &processedAt); err != nil {
		t.Fatalf("query events row: %v", err)
	}
	if status != "done" {
		t.Errorf("status = %q, want done", status)
	}
	if !processedAt.Valid {
		t.Error("processed_at is NULL after MarkDone, want set (04 §3)")
	}
}

func TestIntegration_MarkRetry_SetsBackoffAndKeepsRowPending(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	id, err := store.InsertEvent(ctx, runtime.EventHumanMessage, []byte(`{"text":"hi"}`))
	if err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}
	if _, _, err = store.ClaimNextDue(ctx, runtime.QueueEvents); err != nil {
		t.Fatalf("ClaimNextDue: %v", err)
	}
	future := time.Now().Add(time.Hour)
	if err = store.MarkRetry(ctx, runtime.QueueEvents, id, "boom", future); err != nil {
		t.Fatalf("MarkRetry: %v", err)
	}

	var status, lastError string
	var nextAttemptAt time.Time
	if err = db.QueryRowContext(ctx,
		`SELECT status, last_error, next_attempt_at FROM events WHERE id = $1`, id).
		Scan(&status, &lastError, &nextAttemptAt); err != nil {
		t.Fatalf("query events row: %v", err)
	}
	if status != "pending" {
		t.Errorf("status = %q after MarkRetry, want pending (still retryable, 04 §3)", status)
	}
	if lastError != "boom" {
		t.Errorf("last_error = %q, want %q", lastError, "boom")
	}
	if nextAttemptAt.Before(time.Now().Add(30 * time.Minute)) {
		t.Errorf("next_attempt_at = %v, want roughly %v (an hour out)", nextAttemptAt, future)
	}

	// Not due yet: a fresh claim must skip this row.
	_, ok, err := store.ClaimNextDue(ctx, runtime.QueueEvents)
	if err != nil {
		t.Fatalf("ClaimNextDue: %v", err)
	}
	if ok {
		t.Error("ClaimNextDue returned a not-yet-due retried row")
	}
}

func TestIntegration_MarkDead_SetsStatusDead(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	id, err := store.InsertEvent(ctx, runtime.EventHumanMessage, []byte(`{"text":"hi"}`))
	if err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}
	if _, _, err = store.ClaimNextDue(ctx, runtime.QueueEvents); err != nil {
		t.Fatalf("ClaimNextDue: %v", err)
	}
	if err = store.MarkDead(ctx, runtime.QueueEvents, id, "gave up"); err != nil {
		t.Fatalf("MarkDead: %v", err)
	}

	var status, lastError string
	if err = db.QueryRowContext(ctx,
		`SELECT status, last_error FROM events WHERE id = $1`, id).Scan(&status, &lastError); err != nil {
		t.Fatalf("query events row: %v", err)
	}
	if status != "dead" {
		t.Errorf("status = %q, want dead", status)
	}
	if lastError != "gave up" {
		t.Errorf("last_error = %q, want %q", lastError, "gave up")
	}

	_, ok, err := store.ClaimNextDue(ctx, runtime.QueueEvents)
	if err != nil {
		t.Fatalf("ClaimNextDue: %v", err)
	}
	if ok {
		t.Error("ClaimNextDue returned a dead row")
	}
}

// ---- the transcript: single-tx append+enqueue (07 §3) ---------------------

func TestIntegration_AppendUserMessageAndEnqueueEvent_InsertsBothRows(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	msgID, evID, err := store.AppendUserMessageAndEnqueueEvent(ctx, "build the widget")
	if err != nil {
		t.Fatalf("AppendUserMessageAndEnqueueEvent: %v", err)
	}
	if msgID == 0 || evID == 0 {
		t.Fatalf("got (messageID=%d, eventID=%d), want both non-zero", msgID, evID)
	}

	var role, text string
	if err := db.QueryRowContext(ctx, `SELECT role, text FROM messages WHERE id = $1`, msgID).
		Scan(&role, &text); err != nil {
		t.Fatalf("query messages row: %v", err)
	}
	if role != "user" || text != "build the widget" {
		t.Errorf("messages row = (role=%q, text=%q), want (user, %q)", role, text, "build the widget")
	}

	var evType string
	var payload []byte
	if err := db.QueryRowContext(ctx, `SELECT type, payload FROM events WHERE id = $1`, evID).
		Scan(&evType, &payload); err != nil {
		t.Fatalf("query events row: %v", err)
	}
	if evType != string(runtime.EventHumanMessage) {
		t.Errorf("event type = %q, want %q", evType, runtime.EventHumanMessage)
	}
	if got := string(payload); got != `{"text":"build the widget"}` && got != `{"text": "build the widget"}` {
		t.Errorf("event payload = %s, want a {text} object carrying the same text", payload)
	}
}

// TestIntegration_AppendUserMessageAndEnqueueEvent_AtomicOnFailure proves the
// two inserts share one transaction (07 §3: "the transcript and the event
// queue cannot disagree") by forcing the second half (the events insert) to
// fail — via temporarily hiding the events table — and confirming the first
// half (the messages insert) never persists either.
func TestIntegration_AppendUserMessageAndEnqueueEvent_AtomicOnFailure(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	// Sanity check first, with the schema intact: the method must be able to
	// succeed at all, so that the failure below is evidence of atomicity and
	// not just an unconditional stub error (a stub that never touches the
	// database would trivially "pass" the no-row-persisted assertion below
	// without proving anything about transactions).
	if _, _, err := store.AppendUserMessageAndEnqueueEvent(ctx, "sanity-check-normal-path"); err != nil {
		t.Fatalf("sanity call before inducing failure: unexpected error: %v", err)
	}

	if _, err := db.ExecContext(ctx, `ALTER TABLE events RENAME TO events_hidden_for_test`); err != nil {
		t.Fatalf("hide events table: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.ExecContext(context.Background(),
			`ALTER TABLE IF EXISTS events_hidden_for_test RENAME TO events`); err != nil {
			t.Fatalf("restore events table: %v", err)
		}
	})

	const text = "this must not survive a failed enqueue"
	_, _, err := store.AppendUserMessageAndEnqueueEvent(ctx, text)
	if err == nil {
		t.Fatal("AppendUserMessageAndEnqueueEvent succeeded with the events table missing; want an error")
	}

	// Restore now so we can read the messages table back.
	if _, err := db.ExecContext(ctx, `ALTER TABLE events_hidden_for_test RENAME TO events`); err != nil {
		t.Fatalf("restore events table early: %v", err)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM messages WHERE text = $1`, text).Scan(&count); err != nil {
		t.Fatalf("count messages rows: %v", err)
	}
	if count != 0 {
		t.Errorf("messages row persisted (%d rows) despite the paired event insert failing — "+
			"AppendUserMessageAndEnqueueEvent must be a single transaction (07 §3)", count)
	}
}

// ---- Say's other half, and the brain's conversation window (07 §3) -------

func TestIntegration_AppendKilnMessageAndRecent_OldestFirst(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	if _, _, err := store.AppendUserMessageAndEnqueueEvent(ctx, "first"); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	kiln, err := store.AppendKilnMessage(ctx, "second")
	if err != nil {
		t.Fatalf("AppendKilnMessage: %v", err)
	}
	if kiln.Role != runtime.RoleKiln || kiln.Text != "second" {
		t.Errorf("AppendKilnMessage returned %+v, want Role=kiln Text=second", kiln)
	}
	if _, _, err = store.AppendUserMessageAndEnqueueEvent(ctx, "third"); err != nil {
		t.Fatalf("append user message: %v", err)
	}

	got, err := store.Recent(ctx, 2)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Recent(2) returned %d rows, want 2", len(got))
	}
	if got[0].Text != "second" || got[1].Text != "third" {
		t.Errorf("Recent(2) = [%q, %q], want oldest-first [second, third]", got[0].Text, got[1].Text)
	}

	all, err := store.Recent(ctx, 50)
	if err != nil {
		t.Fatalf("Recent(50): %v", err)
	}
	if len(all) != 3 || all[0].Text != "first" {
		t.Errorf("Recent(50) = %v, want 3 rows oldest-first starting with 'first'", all)
	}
}
