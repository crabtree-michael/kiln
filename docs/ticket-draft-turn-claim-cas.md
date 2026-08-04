# Ticket draft — give the agent turn machine a durable claim

Drafted 2026-08-04 off `docs/root-cause-2026-08-03-duplicate-instances.md` §6 P0 item 2, revised by
`docs/root-cause-2026-08-04-followup.md` §2. Deliberately kept separate from
[`ticket-draft-advisory-lock.md`](ticket-draft-advisory-lock.md) — the lock does not subsume this.

Paste the title/body below into the board; the rest is working detail.

---

## Title

Claim an agent turn before starting it, instead of acting on a stale snapshot

## Body

`stepStartTurn` calls the provider **before** it writes the state change, and it decides from a row
snapshot it never re-reads:

```go
// backend/internal/agent/service.go:611-636
func (s *Service) stepStartTurn(ctx context.Context, provider Provider, prefix string, t Turn) {
    w, err := s.ensureWorker(ctx, provider, prefix, t.WorkerID)   // provider round trip
    ...
    ref, err := provider.StartTurn(ctx, w, conversation, t.Message, fresh)  // side effect FIRST
    ...
    t.Phase = PhaseTurnStarted
    s.update(ctx, t)                                              // state change AFTER
}
```

The row sits at `worker_ready` for the whole of that — `ensureWorker` plus up to the 12 s
`agentSendTimeout` — and `StartTurn` is not idempotent (`fresh` mints a brand-new Amika session,
and Amika has no request dedupe at all, `05:204`). Any other poller that sees `worker_ready` in that
gap launches a second `claude` process in the same working directory.

**Measured: 7 of 47 turns (14.9 %) were started twice** in 15.4 h on 2026-08-04, 3 of them
`fresh:true` — two independent sessions editing one tree. 12 more in the preceding 49 h. Full
evidence in the two documents above.

### Two things that are easy to get wrong

**1. The window is a whole poll pass, not one send.** `pollOnce`
(`service.go:505-514`) takes **one** `ListNonTerminal` snapshot and then iterates it serially,
blocking on provider calls per row. Observed duplicate gaps of **12.4 s, 10.0 s and 17.9 s** exceed
the 12 s send timeout, so the second instance decided from a read many seconds stale. A CAS that
re-reads nothing is not a fix.

**2. `s.update` is an unconditional whole-row write.** The second starter overwrites
`provider_turn` with its own session ref on top of the ref the first wrote — so the first session's
handle is *destroyed*, not merely unreferenced. That is why an orphaned `claude` process cannot be
stopped after the fact. A narrow, conditional update is part of the fix, not a nicety.

### The change

Make the transition a compare-and-swap, and re-read inside it:

```sql
UPDATE agent_turns
   SET phase = 'starting'
 WHERE idempotency_key = $1 AND phase = 'worker_ready'
RETURNING idempotency_key, project_id, ticket_id, worker_id, message, provider_turn, attempts
```

- Call `StartTurn` **only** if the CAS returned a row, and derive `fresh` from the **returned**
  `provider_turn`, not from the caller's snapshot — `fresh` is stale by construction under this
  reordering.
- On success, move `starting` → `turn_started` and record the handle in the same narrow update
  (`SET phase, provider_turn WHERE idempotency_key = $1 AND phase = 'starting'`). Do not write back
  the whole snapshot.
- A crash between the CAS and `StartTurn` leaves a `starting` row. That needs a wall-clock sweep to
  recover — which is the turn deadline A3.1 has wanted since the first document, so build it here.

### Why this is still needed after the advisory lock

The lock makes one instance the only one running the loops. It does **not** prevent the
"orphaned at birth" shape: an instance that dies inside the 12 s send window has already caused
Amika to spawn a process and has recorded nothing. Seen twice at the OS level (`idem_key` 8914,
8968), with 39 `agent: persist turn … context canceled` lines in the last 15 h as the proxy for how
often it happens invisibly. The CAS plus the `starting` sweep is what makes that state recoverable.
It also makes the machine correct across ordinary restarts, independent of how many instances run.

### Where the change goes

- `backend/internal/agent/service.go:611-636` — `stepStartTurn`.
- `backend/internal/agent/postgres/store.go` — a new `ClaimForStart` (CAS) and a narrow
  `RecordStarted`; `ListNonTerminal` (`:62`) stays as-is, it is only a work-finder now.
- Migration: add `starting` to the phase domain, plus whatever the sweep needs to age a row out
  (`phase_changed_at`, if there is no usable timestamp already).
- `backend/internal/agent/turn.go:49` — `ProviderWorker` is declared, persisted and never
  set or read. Wire it here; it is the pin that makes a duplicate detectable after the fact.

### Acceptance criteria

- [ ] Two `Service` instances polling one store over one `worker_ready` row produce exactly one
      `StartTurn`.
- [ ] The loser makes no provider call and logs at debug, not as an error.
- [ ] `fresh` is computed from the row read inside the claim, never from the caller's snapshot.
- [ ] A turn stuck at `starting` past a deadline is failed or retried by a sweep, not left forever.
- [ ] A completed turn's `provider_turn` is never overwritten by a later writer holding an older
      snapshot.

### Tests

- Unit, with `-race`: two `Service`s over one store, one row, a provider that blocks inside
  `StartTurn`; assert exactly one call. **This is the test the gate has never had** — note
  `make test-backend` does not pass `-race` at all today (`Makefile:74-82`), which is the reason
  none of this was caught.
- Store integration: the CAS returns a row exactly once for concurrent callers.
- Recovery: a row left at `starting` is aged out by the sweep.
