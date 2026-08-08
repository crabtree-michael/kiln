package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lib/pq"

	"github.com/crabtree-michael/kiln/backend/internal/board"
)

// unmetDependencyExists is the pull's skip test as a correlated subquery: does
// this ticket wait on anything that has not landed? It is spliced into
// NextReadyTicket against the candidate row aliased `tickets`.
//
// Both halves of the WHERE matter. `archived_at IS NULL` is what makes deleting
// a dependency safe: an archived ticket can never reach done, so an edge to one
// must stop counting or everything behind it waits forever. `state <> 'done'` is
// the actual condition being waited on.
const unmetDependencyExists = `
	SELECT 1 FROM ticket_dependencies d
	JOIN tickets dep ON dep.id = d.depends_on_id
	WHERE d.ticket_id = tickets.id
	  AND dep.archived_at IS NULL
	  AND dep.state <> 'done'`

// dependencyRows reads the project's live dependency edges — every edge whose
// dependency still exists — as (ticket, dependency, dependency-is-done) triples
// in insertion order. One query serves both the whole board and a single
// ticket: `onlyTicket` empty means every ticket in the project.
//
// Ordering by created_at then id keeps DependsOn stable across reads, so the
// client is not re-rendering a list that shuffles under it.
func (s *Store) dependencyRows(
	ctx context.Context, projectID string, onlyTicket board.TicketID,
) (_ map[board.TicketID][]board.TicketID, _ map[board.TicketID]int, err error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT d.ticket_id, d.depends_on_id, (dep.state = 'done') AS met
		 FROM ticket_dependencies d
		 JOIN tickets dep ON dep.id = d.depends_on_id AND dep.archived_at IS NULL
		 WHERE d.project_id = $1 AND ($2 = '' OR d.ticket_id::text = $2)
		 ORDER BY d.ticket_id, d.created_at, d.depends_on_id`,
		projectID, string(onlyTicket))
	if err != nil {
		return nil, nil, fmt.Errorf("board/postgres: query dependencies: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("board/postgres: close dependencies: %w", cerr)
		}
	}()

	deps := map[board.TicketID][]board.TicketID{}
	unmet := map[board.TicketID]int{}
	for rows.Next() {
		var ticketID, dependsOn string
		var met bool
		if serr := rows.Scan(&ticketID, &dependsOn, &met); serr != nil {
			return nil, nil, fmt.Errorf("board/postgres: scan dependency: %w", serr)
		}
		id := board.TicketID(ticketID)
		deps[id] = append(deps[id], board.TicketID(dependsOn))
		if !met {
			unmet[id]++
		}
	}
	if rerr := rows.Err(); rerr != nil {
		return nil, nil, fmt.Errorf("board/postgres: iterate dependencies: %w", rerr)
	}
	return deps, unmet, nil
}

// attachDependencies fills DependsOn and UnmetDependencies on every ticket in
// the snapshot from one query over the project's edges. Called after the groups
// are populated, so it walks what is already in memory rather than re-reading.
func (s *Store) attachDependencies(ctx context.Context, projectID string, snap *board.Snapshot) error {
	deps, unmet, err := s.dependencyRows(ctx, projectID, "")
	if err != nil {
		return err
	}
	if len(deps) == 0 {
		return nil
	}
	for _, group := range [][]board.Ticket{snap.Shaping, snap.Ready, snap.Blocked, snap.Working, snap.Done} {
		for i := range group {
			group[i].DependsOn = deps[group[i].ID]
			group[i].UnmetDependencies = unmet[group[i].ID]
		}
	}
	return nil
}

// SetDependency records or drops the id → dependsOn edge (0013).
//
// Idempotent in both directions, which is what the port promises: ON CONFLICT DO
// NOTHING makes a repeated insert a no-op, and a DELETE that matches nothing
// affects no rows and is not an error. The foreign keys reject an id that is not
// a ticket at all; the live-ticket and cycle checks are the caller's, since it
// holds the row locks.
func (t *tx) SetDependency(
	ctx context.Context, projectID string, id, dependsOn board.TicketID, present bool,
) error {
	if present {
		if _, err := t.sqltx.ExecContext(ctx,
			`INSERT INTO ticket_dependencies (project_id, ticket_id, depends_on_id)
			 VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
			projectID, string(id), string(dependsOn)); err != nil {
			return fmt.Errorf("board/postgres: insert dependency: %w", err)
		}
		return nil
	}
	if _, err := t.sqltx.ExecContext(ctx,
		`DELETE FROM ticket_dependencies
		 WHERE project_id = $1 AND ticket_id = $2 AND depends_on_id = $3`,
		projectID, string(id), string(dependsOn)); err != nil {
		return fmt.Errorf("board/postgres: delete dependency: %w", err)
	}
	return nil
}

// DependencyPathTo walks the edges forward from `from`, breadth-first, looking
// for `target` — "does `from` already wait on `target`?" — and returns the route
// when it finds one.
//
// The recursive CTE carries the path as an array so the answer names the chain,
// and `NOT (dep.depends_on_id = ANY(path))` stops it revisiting a ticket. That
// guard is not decoration: the edge set is acyclic only because this check keeps
// it so, and a ring that somehow existed would otherwise recurse forever.
//
// Archived tickets are not traversed — an edge through one is already dead, so a
// ring that only closes through it is not a real cycle and must not block an
// otherwise legitimate edge.
func (t *tx) DependencyPathTo(
	ctx context.Context, projectID string, from, target board.TicketID,
) ([]board.TicketID, bool, error) {
	row := t.sqltx.QueryRowContext(ctx,
		`WITH RECURSIVE walk(id, path) AS (
			SELECT $2::uuid, ARRAY[$2::uuid]
		  UNION ALL
			SELECT dep.depends_on_id, walk.path || dep.depends_on_id
			FROM ticket_dependencies dep
			JOIN tickets dt ON dt.id = dep.depends_on_id AND dt.archived_at IS NULL
			JOIN walk ON dep.ticket_id = walk.id
			WHERE dep.project_id = $1 AND NOT (dep.depends_on_id = ANY(walk.path))
		 )
		 SELECT path FROM walk WHERE id = $3::uuid LIMIT 1`,
		projectID, string(from), string(target))
	var path pq.StringArray
	if err := row.Scan(&path); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("board/postgres: walk dependencies: %w", err)
	}
	out := make([]board.TicketID, 0, len(path))
	for _, p := range path {
		out = append(out, board.TicketID(p))
	}
	return out, true, nil
}

// Dependents reports whether any live ticket in the project waits on id (0013),
// so ArchiveTicket knows whether removing it freed anything.
func (t *tx) Dependents(ctx context.Context, projectID string, id board.TicketID) (bool, error) {
	var exists bool
	if err := t.sqltx.QueryRowContext(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM ticket_dependencies d
			JOIN tickets w ON w.id = d.ticket_id AND w.archived_at IS NULL
			WHERE d.project_id = $1 AND d.depends_on_id = $2
		 )`, projectID, string(id)).Scan(&exists); err != nil {
		return false, fmt.Errorf("board/postgres: check dependents: %w", err)
	}
	return exists, nil
}
