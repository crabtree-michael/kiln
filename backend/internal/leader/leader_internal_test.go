package leader

import (
	"log/slog"
	"testing"
	"time"
)

// The Elector's behaviour is Postgres semantics and is tested for real in
// leader_integration_test.go — pg_try_advisory_lock has no meaningful fake.
// What is worth pinning down offline is the two things that are pure
// arithmetic: the config defaults, and the shape of the lock key that
// holdsLockSQL's classid/objid reassembly depends on.

func TestNewAppliesDefaults(t *testing.T) {
	e := New(nil, Config{})
	if e.key != LockKey {
		t.Errorf("key = %#x, want the default %#x", e.key, LockKey)
	}
	if e.retry != DefaultRetryInterval {
		t.Errorf("retry = %s, want %s", e.retry, DefaultRetryInterval)
	}
	if e.check != DefaultCheckInterval {
		t.Errorf("check = %s, want %s", e.check, DefaultCheckInterval)
	}
	if e.log == nil {
		t.Error("log is nil; want slog.Default()")
	}
}

func TestNewKeepsExplicitConfig(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	e := New(nil, Config{Key: 42, Retry: time.Second, Check: 2 * time.Second, Log: log})
	if e.key != 42 {
		t.Errorf("key = %d, want 42", e.key)
	}
	if e.retry != time.Second || e.check != 2*time.Second {
		t.Errorf("cadences = %s/%s, want 1s/2s", e.retry, e.check)
	}
	if e.log != log {
		t.Error("log was replaced by the default")
	}
}

// TestLockKeyFitsPgLocksReassembly guards the invariant holdsLockSQL relies on.
// Postgres records a one-argument advisory lock as two oids — the key's high
// half in classid, its low half in objid — and the leader re-verifies its own
// hold by reassembling them with `(classid::bigint << 32) | objid::bigint`.
// That expression overflows bigint, and the verification then errors on every
// tick (so nothing ever leads), if the high half has its top bit set. Changing
// LockKey to something that does not fit must fail here rather than in prod.
func TestLockKeyFitsPgLocksReassembly(t *testing.T) {
	if LockKey <= 0 {
		t.Fatalf("LockKey = %#x, want a positive key", LockKey)
	}
	if high := LockKey >> 32; high >= 1<<31 {
		t.Errorf("LockKey high half = %#x, want < 2^31 so the pg_locks reassembly cannot overflow", high)
	}
}
