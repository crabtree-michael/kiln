//go:build integration

package main

// Cross-tenant isolation for the session reset (11 §3, final-review Finding 1).
// The /debug "Reset session" button is mounted unconditionally in prod behind
// withProject, so ANY authenticated tenant can fire it — a reset MUST therefore
// touch only the caller's project. This test seeds two tenants A and B with rows
// in every one of the eight state tables (including a worker plus a Working
// ticket that references it, so the tickets.worker_id → workers(id) FK forces
// the delete order), runs the REAL resetCoordinator against A, and asserts B's
// rows all survive while A's are gone. It exercises the production dbStateDeleter
// and the real board-store pool reseed; the agent teardown is faked (no sandbox
// provider under test).
//
// Run with:
//
//	TEST_DATABASE_URL=postgres://kiln:kiln@localhost:5432/kiln_test?sslmode=disable \
//	    go test -tags=integration ./cmd/kiln/... -run ResetIsolation
//
// Like the bootstrap test, it uses its own dedicated database (via
// bootstrapTestDB) so it never disturbs the shared kiln_test used by siblings.

import (
	"context"
	"database/sql"
	"testing"

	boardpg "github.com/crabtree-michael/kiln/backend/internal/board/postgres"
)

// noopWorkerResetter satisfies workerResetter without a real sandbox provider:
// the DB scoping is what this test verifies, not sandbox teardown (that is the
// agent module's own concern).
type noopWorkerResetter struct {
	gotProjectID string
}

func (n *noopWorkerResetter) ResetProject(_ context.Context, projectID string) error {
	n.gotProjectID = projectID
	return nil
}

func TestResetIsolation_ResetsOnlyCallerProject(t *testing.T) {
	ctx := context.Background()
	db := bootstrapTestDB(t)

	const projA = "11111111-1111-1111-1111-111111111111"
	const projB = "22222222-2222-2222-2222-222222222222"
	seedProjectState(t, db, projA, "a")
	seedProjectState(t, db, projB, "b")

	// Positive control: both projects have a full set of rows before the reset.
	for _, table := range projectIDTables {
		if n := countFor(t, db, table, projA); n == 0 {
			t.Fatalf("seed check: A has no %s rows", table)
		}
		if n := countFor(t, db, table, projB); n == 0 {
			t.Fatalf("seed check: B has no %s rows", table)
		}
	}

	const poolSize = 3
	agent := &noopWorkerResetter{}
	// nil worker-count resolver: identity isn't wired in this DB-only test, so the
	// reset falls back to poolSize (the deployment default) exactly as it does in a
	// dark-when-unconfigured boot. The configured-count path is unit-tested.
	c := newResetCoordinator(db, agent, boardpg.New(db), poolSize, nil)

	if err := c.Reset(ctx, projA); err != nil {
		t.Fatalf("Reset(A): %v", err)
	}

	// The agent teardown was scoped to A.
	if agent.gotProjectID != projA {
		t.Errorf("agent reset for project %q, want the caller's project A", agent.gotProjectID)
	}

	// A's state is gone from every table EXCEPT workers, which the pool reseed
	// refills to poolSize (a fresh idle pool, exactly as at startup).
	for _, table := range projectIDTables {
		got := countFor(t, db, table, projA)
		want := 0
		if table == "workers" {
			want = poolSize
		}
		if got != want {
			t.Errorf("after reset, A's %s count = %d, want %d", table, got, want)
		}
	}

	// B is entirely untouched — the whole point: A's reset must not delete,
	// mutate, or reseed any of B's rows.
	for _, table := range projectIDTables {
		if got := countFor(t, db, table, projB); got != 1 {
			t.Errorf("after A's reset, B's %s count = %d, want 1 — CROSS-TENANT DATA LOSS", table, got)
		}
	}
}

// seedProjectState inserts exactly one row into each of the eight state tables
// for projectID. tag makes the primary keys that are not uuids (agent_turns
// idempotency_key, steward_pokes ticket_id) unique across the two projects.
func seedProjectState(t *testing.T, db *sql.DB, projectID, tag string) {
	t.Helper()
	ctx := context.Background()
	exec := func(query string, args ...any) {
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("seed %s [%s]: %v", tag, query, err)
		}
	}

	// A worker plus a Working ticket bound to it: the ticket references the
	// worker, so a global TRUNCATE-less delete must remove tickets before workers.
	var workerID string
	if err := db.QueryRowContext(ctx,
		`INSERT INTO workers (id, project_id) VALUES (gen_random_uuid(), $1) RETURNING id`,
		projectID).Scan(&workerID); err != nil {
		t.Fatalf("seed %s worker: %v", tag, err)
	}
	exec(`INSERT INTO tickets (id, title, state, worker_id, project_id)
		VALUES (gen_random_uuid(), 'seed-`+tag+`', 'working', $1, $2)`, workerID, projectID)

	exec(`INSERT INTO outbox (topic, payload, project_id) VALUES ('board.updated', '{}', $1)`, projectID)
	exec(`INSERT INTO messages (role, text, project_id) VALUES ('user', 'msg-`+tag+`', $1)`, projectID)
	exec(`INSERT INTO events (type, payload, project_id) VALUES ('human.message', '{}', $1)`, projectID)
	exec(`INSERT INTO agent_turns (idempotency_key, kind, worker_id, project_id)
		VALUES ($1, 'send', $2, $3)`, keyForTag(tag), workerID, projectID)
	exec(`INSERT INTO notifications (kind, body, project_id) VALUES ('update', 'note-`+tag+`', $1)`, projectID)
	exec(`INSERT INTO steward_pokes (ticket_id, worker_id, poked_at, project_id)
		VALUES ('poke-`+tag+`', 'w', now(), $1)`, projectID)
}

// keyForTag maps a seed tag to a distinct agent_turns idempotency key (its
// bigint primary key must be unique across both projects).
func keyForTag(tag string) int64 {
	if tag == "a" {
		return 1
	}
	return 2
}

func countFor(t *testing.T, db *sql.DB, table, projectID string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM `+table+` WHERE project_id = $1`, projectID).Scan(&n); err != nil {
		t.Fatalf("count %s for %s: %v", table, projectID, err)
	}
	return n
}
