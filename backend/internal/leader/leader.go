// Package leader elects a single owner for the backend's background work
// loops, so that exactly one process acts on pending work even while two are
// alive.
//
// # Why this exists
//
// Render's zero-downtime deploy boots the replacement instance, waits for
// /healthz, cuts traffic over, and only then drains the old one — so two full
// backend processes run concurrently for 67–83 s of every deploy (measured
// across five investigations, docs/root-cause-2026-08-02-* .. -08-04-part5-*).
// Both ran the full set of background loops, and neither the agent turn
// machine nor the event queue has a durable claim strong enough to stop the
// second one acting on work the first is already mid-way through. The result,
// confirmed repeatedly in production, is two independent `claude` sessions
// editing one working tree.
//
// The fix is an ordering one: an instance may run the loops only while it
// holds a fixed Postgres advisory lock. The follower still serves HTTP and
// SSE — it just does no background work — so a deploy's overlap window becomes
// a handoff instead of a race.
//
// # Why an advisory lock rather than a row-based lease
//
// A session-scoped advisory lock is released by Postgres when the session
// ends, for any reason. A clean shutdown releases it explicitly; a SIGKILL,
// OOM or container stop drops the TCP session and Postgres releases it with
// the backend. That is takeover within seconds with no lease, no heartbeat and
// no clock-skew reasoning — and no migration, since advisory locks are session
// state rather than schema.
//
// # Why a pinned connection
//
// *sql.DB is a pool: a lock taken on one pooled connection and released on
// another is a silent no-op. The Elector holds one *sql.Conn for as long as it
// leads and keeps it out of general use. The flip side is that the connection
// dying (pooler restart, network blip) silently drops the lock, so leading is
// not a one-shot check at boot: the Elector re-verifies on every tick that its
// own backend still holds the lock, and cancels the loops the moment it does
// not.
//
// This assumes direct connections to Postgres. Under a transaction-pooling
// proxy (PgBouncer in transaction mode) session-scoped advisory locks are not
// meaningful; the periodic re-verification would then fail and no instance
// would lead. Render's managed Postgres, which this runs against, is a direct
// connection.
package leader

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// LockKey is the fixed 64-bit key the background work loops are gated on: the
// ASCII bytes of "kiln" in the high half, a slot number in the low half. It is
// the one advisory-lock key in the tree; anything else needing a lock should
// take the next slot rather than reuse this one.
//
// The high half must stay below 2^31 so that holdsLockSQL's reassembly of
// classid/objid back into a bigint cannot overflow. 0x6B696C6E satisfies that.
const LockKey int64 = 0x6B696C6E00000001

// Campaign cadences. Retry bounds how long a follower waits before trying the
// lock again — i.e. the worst-case gap between the leader exiting and its
// replacement picking the work up. Check bounds how long a leader whose
// connection has died keeps running the loops before noticing.
const (
	DefaultRetryInterval = 3 * time.Second
	DefaultCheckInterval = 5 * time.Second
)

// unlockTimeout bounds the explicit release on the way out. It runs on a
// context detached from the (already cancelled) shutdown context, so it needs
// a deadline of its own.
const unlockTimeout = 5 * time.Second

// ErrLockLost reports that the advisory lock this process was leading under is
// no longer held by its own backend — the connection died underneath it. The
// loops are stopped and the Elector re-campaigns from scratch.
var ErrLockLost = errors.New("leader: advisory lock no longer held")

// tryLockSQL takes the lock without blocking: true if this session now holds
// it, false if another session does.
const tryLockSQL = `SELECT pg_try_advisory_lock($1)`

// unlockSQL releases it. Returns false (with a server-side warning) if this
// session did not hold it, so it is only ever issued after a successful take.
const unlockSQL = `SELECT pg_advisory_unlock($1)`

// holdsLockSQL re-verifies that *this backend* still holds the lock, which is
// the question a bare "we acquired it once" boolean cannot answer. A single-
// argument advisory lock is recorded with objsubid = 1 and the 64-bit key split
// across classid (high half) and objid (low half); this reassembles it. Issued
// on the pinned connection, so pg_backend_pid() is the session that took it —
// which also makes a dead connection surface here as a query error.
const holdsLockSQL = `
	SELECT EXISTS (
		SELECT 1 FROM pg_locks
		WHERE locktype = 'advisory'
		  AND granted
		  AND objsubid = 1
		  AND pid = pg_backend_pid()
		  AND ((classid::bigint << 32) | objid::bigint) = $1
	)`

// Config configures an Elector. Every field has a working default; Log is the
// only one worth always setting, and it should already carry the process's
// instance id (obs.InstanceKey) so a handoff is legible across the two
// instances' interleaved log streams.
type Config struct {
	Key   int64         // advisory-lock key; defaults to LockKey
	Retry time.Duration // follower retry cadence; defaults to DefaultRetryInterval
	Check time.Duration // leader re-verification cadence; defaults to DefaultCheckInterval
	Log   *slog.Logger  // defaults to slog.Default()
}

// Elector runs a function on exactly one instance at a time.
type Elector struct {
	db    *sql.DB
	key   int64
	retry time.Duration
	check time.Duration
	log   *slog.Logger
}

// New builds an Elector over db, applying Config's defaults.
func New(db *sql.DB, cfg Config) *Elector {
	e := &Elector{db: db, key: cfg.Key, retry: cfg.Retry, check: cfg.Check, log: cfg.Log}
	if e.key == 0 {
		e.key = LockKey
	}
	if e.retry <= 0 {
		e.retry = DefaultRetryInterval
	}
	if e.check <= 0 {
		e.check = DefaultCheckInterval
	}
	if e.log == nil {
		e.log = slog.Default()
	}
	return e
}

// Run campaigns for leadership until ctx is done, running fn for as long as
// this process holds the lock and no longer.
//
// fn receives a context cancelled the instant leadership ends — by shutdown or
// by the lock being lost — and Run does not release the lock until fn has
// returned. That ordering is the guarantee callers depend on: the next
// instance cannot start the loops until this one's have stopped.
//
// Run never returns an error: failing to acquire is the normal state of a
// follower, and a failure to reach Postgres is retried like any other. It
// returns only when ctx is done.
func (e *Elector) Run(ctx context.Context, fn func(context.Context)) {
	standby := false
	for ctx.Err() == nil {
		led, err := e.campaign(ctx, fn)
		switch {
		case err != nil:
			if ctx.Err() == nil {
				e.log.Error("leader.error", "err", err)
			}
			standby = false
		case led:
			standby = false
		case !standby:
			// Standby: another instance owns the background loops. Logged on
			// the transition, not on every poll of it — a follower retries
			// every few seconds for the whole life of the process.
			e.log.Info("leader.standby", "lock_key", e.key)
			standby = true
		}
		select {
		case <-ctx.Done():
		case <-time.After(e.retry):
		}
	}
}

// campaign takes one shot at the lock. It reports whether this process led at
// all; when it did, campaign only returns once leadership has ended and the
// lock is released.
func (e *Elector) campaign(ctx context.Context, fn func(context.Context)) (bool, error) {
	// A pinned connection, not the pool: the lock lives on this exact session.
	conn, err := e.db.Conn(ctx)
	if err != nil {
		return false, fmt.Errorf("leader: pin connection: %w", err)
	}
	acquired, err := e.tryLock(ctx, conn)
	if err != nil || !acquired {
		e.closeConn(conn)
		return false, err
	}
	e.log.Info("leader.acquired", "lock_key", e.key)

	lostErr := e.lead(ctx, conn, fn)

	// The loops have stopped by here — lead does not return until they have —
	// so releasing now can never hand a live overlap to the next instance.
	e.unlock(ctx, conn)
	e.closeConn(conn)
	if lostErr != nil {
		e.log.Warn("leader.lost", "lock_key", e.key, "err", lostErr)
	} else {
		e.log.Info("leader.released", "lock_key", e.key)
	}
	return true, nil
}

// lead runs fn under the held lock, then cancels it and waits for it to drain.
// It returns nil for a clean end (ctx cancelled, or fn returning by itself)
// and the reason when the lock was lost underneath us.
func (e *Elector) lead(ctx context.Context, conn *sql.Conn, fn func(context.Context)) error {
	loopCtx, stop := context.WithCancel(ctx)
	defer stop()

	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(loopCtx)
	}()

	err := e.watch(ctx, conn, done)

	stop()
	<-done
	return err
}

// watch blocks until leadership should end, re-verifying on each tick that the
// pinned session still holds the lock. A verification that errors is treated
// as lost: the honest reading of "we can no longer confirm we own this" is
// that we must stop, not that we may carry on.
func (e *Elector) watch(ctx context.Context, conn *sql.Conn, done <-chan struct{}) error {
	ticker := time.NewTicker(e.check)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-done:
			// fn returned on its own; release and re-campaign.
			return nil
		case <-ticker.C:
			held, err := e.holdsLock(ctx, conn)
			switch {
			case err != nil:
				return fmt.Errorf("leader: verify advisory lock: %w", err)
			case !held:
				return ErrLockLost
			}
		}
	}
}

// tryLock takes the advisory lock on conn without blocking.
func (e *Elector) tryLock(ctx context.Context, conn *sql.Conn) (bool, error) {
	var acquired bool
	if err := conn.QueryRowContext(ctx, tryLockSQL, e.key).Scan(&acquired); err != nil {
		return false, fmt.Errorf("leader: try advisory lock: %w", err)
	}
	return acquired, nil
}

// holdsLock reports whether the pinned session still holds the lock.
func (e *Elector) holdsLock(ctx context.Context, conn *sql.Conn) (bool, error) {
	var held bool
	if err := conn.QueryRowContext(ctx, holdsLockSQL, e.key).Scan(&held); err != nil {
		return false, fmt.Errorf("leader: read pg_locks: %w", err)
	}
	return held, nil
}

// unlock releases the lock explicitly, on a context detached from the caller's
// so a shutdown-cancelled ctx still gets the release out — that is what turns a
// deploy's handoff from "within a few seconds" into "immediately". A failure
// here is not fatal: the lock dies with the session either way.
func (e *Elector) unlock(ctx context.Context, conn *sql.Conn) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), unlockTimeout)
	defer cancel()
	var released bool
	if err := conn.QueryRowContext(ctx, unlockSQL, e.key).Scan(&released); err != nil {
		e.log.Warn("leader.unlock_failed", "lock_key", e.key, "err", err)
		return
	}
	if !released {
		e.log.Warn("leader.unlock_not_held", "lock_key", e.key)
	}
}

// closeConn returns the pinned connection to the pool. It must never run
// before unlock: Close makes the connection reusable, and a session-scoped
// lock left held would ride along on whatever query picks it up next.
func (e *Elector) closeConn(conn *sql.Conn) {
	if err := conn.Close(); err != nil {
		e.log.Warn("leader.close_conn_failed", "err", err)
	}
}
