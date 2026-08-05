# Ticket draft — mark a queue entry's outcome on a context that shutdown cannot cancel

Drafted 2026-08-05 off `docs/root-cause-2026-08-05-part6.md` §3 step 4 and §8 item 1.

> **Status: implemented in the same change that added this file.** Kept as the written record of
> what the fix is for and what it deliberately does not cover. The one thing implementation added
> beyond the draft is in "The change" below: `graph.run` returned on the `srv.Shutdown` error before
> joining the background loops, so the detached write had no window to land in.

Paste the title/body below into the board; the rest is working detail.

---

## Title

A deploy-killed brain pass records no failure, so its event replays one second later

## Body

`Worker.process` marks a queue entry's outcome on **the same context it handled the entry with**
(`backend/internal/runtime/worker.go:165-180`):

```go
func (w *Worker) process(ctx context.Context, e Entry) {
	handleErr := w.safeHandle(ctx, e)
	if handleErr == nil {
		if err := w.store.MarkDone(ctx, w.queue, e.ID); err != nil { ... }
		return
	}
	if e.Attempts >= MaxAttempts {
		w.retire(ctx, e, handleErr)
		return
	}
	next := w.clock.Now().Add(backoff(e.Attempts))
	if err := w.store.MarkRetry(ctx, w.queue, e.ID, handleErr.Error(), next); err != nil { ... }
}
```

On shutdown that context is cancelled, so the handler fails *and* the outcome write fails. Both are
logged and neither is retried. The entry is left exactly as the claim left it: `status = 'pending'`,
`attempts` incremented, `last_error` empty, and `next_attempt_at` = **claim time + 1 s** (the claim's
`least(power(2, attempts), 60)` reading the pre-update `attempts = 0`,
`runtime/postgres/store.go:110`). The next leader re-claims it the moment it acquires the lock.

**Measured in prod over 2026-08-04T13:47Z → 2026-08-05T13:40Z (14 deploys):** 4 of 103 events were
re-processed this way. All 4 logged
`runtime: mark retry ... err="runtime/postgres: mark retry: context canceled"`, and all 4 were
re-claimed **12–402 ms** after the successor's `leader.acquired`. Full evidence in part 6 §2–§4.

The replay is not free. A brain pass has no mid-pass checkpoint, and `create_ticket`, `say` and
`post_update` have no idempotency guard, so a re-run repeats whatever the dead pass already
committed. 3 of the 4 sent the user **two messages for one input**; one (event 2121) had already
created two tickets and posted an update before dying, and avoided creating two more only because
the model listed the board and chose not to.

`MarkDone` at `:168` has the identical exposure and is worse: a pass that *succeeded* and is
cancelled before `MarkDone` lands replays in full. Zero occurrences in the measured window — there
were no `runtime: mark done` errors at all — but the path is the same one line.

### The change

Mark the outcome on a context detached from cancellation, with its own short timeout. The idiom is
already used twice in-tree — `cmd/kiln/wiring.go:791` and `internal/leader/leader.go:283`, the
latter with a 5 s `unlockTimeout` that is the right order of magnitude here:

```go
markCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), markTimeout)
defer cancel()
```

Applies to all three status writes: `MarkDone` (`:168`), `MarkRetry` (`:178`), and `MarkDead` in
`retire` (`:202`). `Worker` backs both queues, so this covers events and outbox alike.

**One judgment call to make explicitly:** `retire` also runs `w.deadLetter(ctx, ...)` at `:205` —
the notify + system-error `say` that tells the user an event was abandoned. It is a side effect, not
a status write, so detaching it is a separate decision. The recommendation is to detach it on the
same timeout: `MarkDead` already made the entry terminal, so a cancelled dead-letter is silent data
loss with no replay to catch it. Today it fails during shutdown unconditionally, so detaching is
strictly better than the status quo — but call it out in review rather than sliding it in.

*Resolved as implemented:* detached, on the same context, with the reasoning recorded in `retire`'s
doc comment.

**What implementation added.** The worker-side change alone would not have worked in prod.
`graph.run` (`cmd/kiln/wiring.go`) did:

```go
if err := srv.Shutdown(shutdownCtx); err != nil {
    return fmt.Errorf("kiln: http shutdown: %w", err)   // <- always taken in prod
}
select {
case <-loopsStopped:                                    // <- never reached
...
```

Every observed prod exit takes that error path (§2's 12/12), so the process returned — and exited —
without ever joining the background loops. A detached mark write would have been racing process
exit. The fix holds the drain error, joins the loops, then returns it.

### Deliberately not in scope

Part 6 §8 lists three follow-ups that this ticket does **not** address. They are independent and
should be separate work:

- **`ticket-draft-sse-shutdown.md`** — the `srv.Shutdown` that never completes (12/12 instance exits
  in the window are `http shutdown: context deadline exceeded`). That is what turns a deploy into a
  hard kill in the first place. This ticket makes the kill survivable; it does not stop it.
- **`ticket-draft-queue-visibility-timeout.md`** — the 1 s claim push-out. After this ticket a
  *recorded* failure retries on the D8 schedule, which is the common case; the 1 s lease still
  governs a process that dies without reaching `process` at all.
- **Idempotent replay** — an idempotency key on `create_ticket`/`post_update`/`say`, and amending
  the replay-safety claim at `internal/brain/service.go:87-91`, which currently covers only
  transitions. This ticket reduces how often a replay happens; it does not make one safe.

### Acceptance criteria

- [ ] An entry whose handler fails because the process is shutting down has its failure **recorded**:
      `last_error` set and `next_attempt_at` on the `backoff(attempts)` schedule, not the claim's.
- [ ] An entry whose handler **succeeded** before shutdown is marked done and is not re-claimed.
- [ ] An entry at `MaxAttempts` is marked dead during shutdown, and the dead-letter action's
      behaviour is whatever review decided above — documented either way.
- [ ] The outcome write cannot hang shutdown indefinitely: it is bounded by its own timeout.
- [ ] Both queues are covered (one `Worker`, no per-queue special-casing).

### Tests

- Worker unit: handle with a cancelled context; assert `MarkRetry` is still called with the
  `backoff(attempts)` due time and the handler's error. Same for `MarkDone` on the success path and
  `MarkDead` at `MaxAttempts`. The existing fakes in `internal/runtime/worker_test.go` already count
  these calls (see the `MaxAttempts=8` assertion at `worker_test.go:158`).
- Worker unit: a store whose mark call blocks does not block past `markTimeout`.
- Store integration: after a cancelled-context `MarkRetry`, the row is not claimable until the
  backoff elapses — the observable that failed in prod.
