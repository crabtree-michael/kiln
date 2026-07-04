// Package postgres is the agent module's store adapter over its one table,
// agent_turns (05 §7) — the module owns this table and the migrations in
// ./migrations; it is adapter-layer state, never board state (03 I8). Pure
// adapter: the machine's rules live in the agent service.
package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
	"github.com/crabtree-michael/kiln/backend/internal/pgutil"
)

// columns is the full agent_turns projection every read scans.
const columns = `idempotency_key, kind, ticket_id, worker_id, message, phase,
	provider_worker, provider_turn, attempts, last_error, created_at, updated_at`

// Store implements agent.Store over Postgres.
type Store struct {
	db *sql.DB
}

var _ agent.Store = (*Store)(nil)

// New wraps an open connection pool; migrations are applied separately at
// startup (tooling TBD — 02 §14).
func New(db *sql.DB) *Store { return &Store{db: db} }

// Record inserts the machine's initial row; a repeated idempotency key is a
// no-op reported as created=false (05 §7).
func (s *Store) Record(ctx context.Context, t agent.Turn) (bool, error) {
	providerTurn, err := marshalTurnRef(t.ProviderTurn)
	if err != nil {
		return false, err
	}
	const q = `INSERT INTO agent_turns
		(idempotency_key, kind, ticket_id, worker_id, message, phase,
		 provider_worker, provider_turn, attempts, last_error)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (idempotency_key) DO NOTHING`
	res, err := s.db.ExecContext(ctx, q,
		t.IdempotencyKey, string(t.Kind), pgutil.NullString(t.TicketID), t.WorkerID, t.Message,
		phaseValue(t.Phase), pgutil.NullString(t.ProviderWorker), providerTurn, t.Attempts, pgutil.NullString(t.LastError))
	if err != nil {
		return false, fmt.Errorf("agent/postgres: record: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("agent/postgres: record rows affected: %w", err)
	}
	return n == 1, nil
}

// ListNonTerminal returns every row the poller must advance — phase <> done,
// including failed, which still owes its error event (05 §5, §7).
func (s *Store) ListNonTerminal(ctx context.Context) ([]agent.Turn, error) {
	const q = `SELECT ` + columns + ` FROM agent_turns WHERE phase <> 'done' ORDER BY idempotency_key`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("agent/postgres: list non-terminal: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			slog.ErrorContext(ctx, "agent/postgres: close list rows", "err", cerr)
		}
	}()
	var turns []agent.Turn
	for rows.Next() {
		t, scanErr := scanTurn(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		turns = append(turns, t)
	}
	if rerr := rows.Err(); rerr != nil {
		return nil, fmt.Errorf("agent/postgres: iterate rows: %w", rerr)
	}
	return turns, nil
}

// Update persists one machine step (05 §5, §7).
func (s *Store) Update(ctx context.Context, t agent.Turn) error {
	providerTurn, err := marshalTurnRef(t.ProviderTurn)
	if err != nil {
		return err
	}
	const q = `UPDATE agent_turns
		SET phase = $2, provider_worker = $3, provider_turn = $4,
		    attempts = $5, last_error = $6, updated_at = now()
		WHERE idempotency_key = $1`
	if _, err := s.db.ExecContext(ctx, q,
		t.IdempotencyKey, phaseValue(t.Phase), pgutil.NullString(t.ProviderWorker),
		providerTurn, t.Attempts, pgutil.NullString(t.LastError)); err != nil {
		return fmt.Errorf("agent/postgres: update: %w", err)
	}
	return nil
}

// LatestForWorker returns a worker's newest operation row (05 §2.1, §3).
func (s *Store) LatestForWorker(ctx context.Context, workerID string) (agent.Turn, bool, error) {
	const q = `SELECT ` + columns + `
		FROM agent_turns WHERE worker_id = $1 ORDER BY idempotency_key DESC LIMIT 1`
	t, err := scanTurn(s.db.QueryRowContext(ctx, q, workerID))
	if errors.Is(err, sql.ErrNoRows) {
		return agent.Turn{}, false, nil
	}
	if err != nil {
		return agent.Turn{}, false, err
	}
	return t, true, nil
}

// scanTurn reads one agent_turns row in `columns` order.
func scanTurn(sc pgutil.RowScanner) (agent.Turn, error) {
	var (
		t              agent.Turn
		kind, phase    string
		ticket         sql.NullString
		providerWorker sql.NullString
		lastError      sql.NullString
		providerTurn   []byte
	)
	if err := sc.Scan(
		&t.IdempotencyKey, &kind, &ticket, &t.WorkerID, &t.Message, &phase,
		&providerWorker, &providerTurn, &t.Attempts, &lastError, &t.CreatedAt, &t.UpdatedAt,
	); err != nil {
		return agent.Turn{}, fmt.Errorf("agent/postgres: scan turn: %w", err)
	}
	t.Kind = agent.Kind(kind)
	t.Phase = agent.Phase(phase)
	t.TicketID = ticket.String
	t.ProviderWorker = providerWorker.String
	t.LastError = lastError.String
	if len(providerTurn) > 0 {
		var ref agent.TurnRef
		if err := json.Unmarshal(providerTurn, &ref); err != nil {
			return agent.Turn{}, fmt.Errorf("agent/postgres: decode provider_turn: %w", err)
		}
		t.ProviderTurn = &ref
	}
	return t, nil
}

// marshalTurnRef renders provider_turn as json text (NULL when absent).
func marshalTurnRef(ref *agent.TurnRef) (sql.NullString, error) {
	if ref == nil {
		return sql.NullString{}, nil
	}
	b, err := json.Marshal(ref)
	if err != nil {
		return sql.NullString{}, fmt.Errorf("agent/postgres: encode provider_turn: %w", err)
	}
	return sql.NullString{String: string(b), Valid: true}, nil
}

// phaseValue defaults an unset phase to recorded (the table's own default).
func phaseValue(p agent.Phase) string {
	if p == "" {
		return string(agent.PhaseRecorded)
	}
	return string(p)
}
