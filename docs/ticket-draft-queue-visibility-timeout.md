# Ticket draft — separate the events queue's visibility timeout from its retry backoff

Drafted 2026-08-04 off `docs/root-cause-2026-08-03-duplicate-instances.md` §6 P0 item 3, with fresh
measurements from `docs/root-cause-2026-08-04-followup.md` §1.3.

Paste the title/body below into the board; the rest is working detail.

---

## Title

A fresh event's lease is 1 second, so an in-flight brain pass is openly re-claimable

## Body

`ClaimNextDue` pushes the due date forward using the **pre-update** attempts count
(`backend/internal/runtime/postgres/store.go:110`, and identically at `:127` for the outbox):

```sql
next_attempt_at = now() + least(power(2, attempts)::bigint, 60) * interval '1 second'
```

For a fresh row `attempts = 0`, so `power(2,0) = 1` — **a one-second visibility timeout**. A brain
pass takes 10–60 s. From one second after the claim until `MarkDone`, the row is claimable by any
other dispatcher.

The only other guard is the `busy` set in `runtime/worker.go:97-150`, which is a
`map[string]struct{}` **local to one `Worker.Run` goroutine in one process**. A second process's
busy set is empty by construction.

**Measured: 11 of 169 events (6.5 %) were claimed twice in 15.4 h, all 11 cross-instance**, on top
of 31 in the preceding 49 h. Each double claim is a full duplicate brain pass: two independent LLM
decisions on one event, two `post_update`s to the user in different words, and — confirmed — two
differently-worded `send_to_agent` instructions to one ticket. That is where the duplicate-agent
fan-out *starts*; the turn machine then doubles it again.

### Why this is not fixed by the advisory lock

It is a real bug with one instance. Any pass that outruns its own lease is one crash-recovery away
from the same double execution, and the comment at `store.go:83-85` — "the dispatcher always marks
it well before then" — is simply false: event 1877 was re-claimed **1.29 s** into a live pass.
Fix that comment as part of this ticket.

The outbox worker is the control that proves it is timing, not design: **byte-identical claim SQL,
0 duplicates in 333**, purely because outbox handlers finish inside the one-second lease. Any outbox
handler that ever gets slow inherits this silently.

### The change

The two concepts are conflated and need separating:

- **Lease / visibility timeout** — how long a claimed row is invisible to other dispatchers.
  Should be ≳2× the longest expected handler (~180 s for a brain pass), or better, explicit.
- **Retry backoff** — how long to wait after a *recorded failure*. This is what
  `least(power(2, attempts), 60)` is actually for, and it should stay on the `MarkRetry` path.

Preferred shape, if a migration is acceptable: claim by status — add `status = 'in_flight'` set by
the claim and cleared by `MarkDone`/`MarkRetry`, with a reaper for rows stuck in flight past a
deadline. This makes the invariant explicit rather than time-derived, and gives the alert below
something exact to key on.

Minimum viable without a migration: floor the claim's push-out at a lease constant
(`greatest(<lease>, least(power(2, attempts), 60))`), and leave `MarkRetry` computing the real
backoff as it already does.

Also downgrade the type comment in `runtime/worker.go` from "*is* the single-writer-per-project
constraint realized in-process" to what `busy` actually is: an in-process concurrency limiter. The
spec-level claim (04 §4, D3) rests on single-instance operation, which does not hold during a
deploy — see the follow-up doc §3.

### Note on the alert

The companion detector (part 3 rec #7) should fire on **a second claim of an event still in
flight**, because that is one level up from the fan-out and catches the whole chain. Do **not** key
it on a ≈1 s gap: the observed gaps are **1.0–19.9 s**. The lease makes the re-claim possible; when
it lands is set by the other dispatcher's availability.

### Acceptance criteria

- [ ] An event claimed and held for 60 s is not re-claimable by another dispatcher.
- [ ] A genuinely failed event still retries on the D8 exponential schedule — the backoff behaviour
      the current expression provides is preserved on the `MarkRetry` path.
- [ ] A dispatcher that dies mid-pass has its row recovered (by lease expiry or reaper), with no
      change to the at-least-once guarantee `03` §5 relies on.
- [ ] `MaxAttempts` is not consumed by a claim that never recorded a failure, or the erosion is
      explicitly accepted and documented.
- [ ] The `store.go:83-85` comment and the `worker.go` type comment say what the code does.

### Tests

- Store integration: claim a row, hold it past 1 s, assert a second `ClaimNextDue` does not return
  it; assert it *is* returned after the lease expires.
- Retry: a `MarkRetry`'d row reappears on the exponential schedule, unchanged from today.
- Both queues — the outbox shares the SQL and is exposed identically.
