# Ticket draft — close the SSE streams on shutdown so the process drains cleanly

Drafted 2026-08-04 off `docs/root-cause-2026-08-03-render-logs.md` §3 (P0 rec #1), with frequency
re-measured in `docs/root-cause-2026-08-04-followup.md` §4 M2.

Paste the title/body below into the board; the rest is working detail.

---

## Title

Graceful shutdown always times out on the SSE board stream, so every drain is a hard kill

## Body

`run()` does a textbook graceful shutdown (`backend/cmd/kiln/wiring.go:784-791`, with
`shutdownTimeout = 15 * time.Second` at `:56`):

```go
<-ctx.Done()
shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
defer cancel()
if err := srv.Shutdown(shutdownCtx); err != nil {
    return fmt.Errorf("kiln: http shutdown: %w", err)
}
```

`http.Server.Shutdown` stops accepting connections and then **waits for active connections to go
idle — it does not cancel in-flight request contexts.** `Hub.ServeStream`
(`backend/internal/api/hub.go:107-145`) is an unbounded loop that returns only when the *client*
disconnects or a write fails. Nothing in the shutdown path signals it.

So whenever a browser has `/api/stream` open — the normal state when anyone has the app in front of
them — `Shutdown` burns the full 15 s and returns `DeadlineExceeded`, and the process exits hard
with in-flight work still running:

```
{"level":"ERROR","msg":"kiln exited with error","err":"kiln: http shutdown: context deadline exceeded"}
```

**Measured: 6 of 13 deploys** in 15.4 h on 2026-08-04 (and 17 of 18 in an earlier, busier 49 h
window — the rate tracks whether a client is connected, which is why it looks intermittent and is
in fact deterministic given one).

### The consequences

Every hard exit produces a `context canceled` cascade — in the last 15.4 h: **39**
`agent: persist turn … context canceled`, 4 `runtime.event.failed … brain: anthropic messages.new:
context canceled`, 4 `runtime: mark retry … context canceled`, 3 `agent: persist turn … sql:
database is closed`.

Two of those matter beyond the noise:

- **It creates orphaned agent processes.** An instance cancelled inside the 12 s `agentSendTimeout`
  has already caused Amika to spawn a `claude` process and never records it — no turn row, no
  session handle, nothing that can stop it. Confirmed twice at the OS level (`idem_key` 8914,
  8968). The 39 `persist turn` cancellations are the proxy for how often this happens invisibly.
- **It burns one of `MaxAttempts = 8`** per kill on an event that never failed (`runtime/queue.go:56`;
  `ClaimNextDue` increments on claim). No event has dead-lettered yet, but the margin is being eaten
  silently.

It also throws away a paid brain LLM call mid-flight several times a day.

### Secondary, but the reason this is P1 rather than P3

These 15 seconds are **15 of the 68–83 seconds** in which two backend instances are both
orchestrating (see the duplicate-instances doc §2). Fixing this shortens every two-headed window by
roughly a fifth. It does not close the window — that is
[`ticket-draft-advisory-lock.md`](ticket-draft-advisory-lock.md) — and this ticket should not be
described as fixing the duplicate-instruction bug.

### The change

Either is fine; pick one:

1. `srv.RegisterOnShutdown(func() { close(h.done) })`, and add `case <-h.done: return` to
   `ServeStream`'s select.
2. Give the server a `BaseContext` derived from a cancellable context and cancel it just before
   `Shutdown`, so every request context — streams included — fires.

(1) is narrower and only affects the streams; (2) is more general but cancels *all* in-flight
requests, so check nothing depends on a normal request completing during the drain.

### Acceptance criteria

- [ ] With a client holding `/api/stream` open, `Shutdown` completes in well under a second.
- [ ] `kiln exited with error: http shutdown: context deadline exceeded` no longer appears after a
      deploy.
- [ ] Connected clients see the stream close and reconnect to the new instance normally — no
      user-visible hang.
- [ ] The `context canceled` cascade above disappears from post-deploy logs.

### Tests

- Unit: a `ServeStream` handler returns when the shutdown signal fires, without a client
  disconnect.
- Integration: start the server, open a stream, call `Shutdown`, assert it returns without hitting
  the timeout.
