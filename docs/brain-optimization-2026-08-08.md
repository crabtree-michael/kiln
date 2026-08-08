# Brain optimization — second pass, findings and proposal

**Date:** 2026-08-08 · **Service:** `srv-d953nmcvikkc73d8aq60` (`kiln`, prod)
**Basis:** source read of the whole model path at `22f48c6`, cross-referenced against the
measured window in [`brain-optimization-2026-08-05.md`](brain-optimization-2026-08-05.md)
**Status:** investigation + proposal. Nothing here is implemented.

> **Now sized against production in
> [`brain-optimization-2026-08-08-measured.md`](brain-optimization-2026-08-08-measured.md).**
> That measurement pass **refutes §1, §2, §3 and §4** on current traffic (respectively: 1 capped
> round in 4,701; max pass 92 s against a 10-minute timeout; 0.94× per-round cost once controlled
> for pass length; 3 output-cap truncations in 666 shell runs), **confirms §5, §6 and §7**, and
> **reframes §8** — the brain's `list_agents` is 2.2/h against the liveness loop's ~1,440/h across
> 4 projects, ~650× larger. §6 is the only finding with a measured double-digit share of spend,
> worth 6.0–17.4%. §1's "currently unmeasurable" is also wrong: the collapse hides `stop_reason`,
> not `output_tokens`.
>
> It also adds **two findings this pass could not see**: 4.1% of all tool calls are exact
> duplicates within a single pass, and `update_ticket` still fails an illegal transition on 10.3%
> of calls — down from 39.5% before recommendation B, which is part of the evidence that A–D
> together cut the tool-call error rate from 7.82% to 1.90%.

> **This pass is a code read, not a log study.** The 2026-08-05 document was reconstructed
> from 17.9k structured log records and its numbers are ground truth. This one walks
> `internal/brain`, `internal/runtime`, `internal/agent`, `internal/repo` and the `cmd/kiln`
> wiring looking for defects the log study could not see — including one (§1) that the logs
> are currently *incapable* of showing. Where a count appears below it is quoted from the
> 08-05 window and labelled as such; nothing new is quantified, because nothing new has been
> measured yet. Sizing these is the first piece of work, not the last.

Recommendations A–D of the previous pass have shipped; E (re-inject the board snapshot) is
still open and is deliberately **not** re-litigated here — §6 is the orthogonal half of that
problem and lands either way.

---

## 0. Headline

| | |
| --- | --- |
| Model call sites in the backend | **1** (`brain/llm.go`) — no hidden second caller |
| Findings | **8** — 4 safe quick fixes, 4 needing their own ticket |
| Largest correctness-shaped defect | §1: a `max_tokens` round is billed in full, discarded silently, and logged as a clean `end_turn` |
| Largest latency-shaped defect | §2: no request timeout — one wedged round can hold a project's event queue ~30 min |
| Cheapest token win | §4: every done verification spends the full 16KB bash cap on an unbounded `git log` |

The shape of this pass differs from the last one. 08-05 found bounded waste distributed
across many rounds ("no smoking gun, four or five small ones"). This pass finds two
**unbounded tail** defects — a silently dropped round and an unbounded call timeout — that
cost nothing on a median pass and a great deal on a bad one. They will not show up in a mean;
they show up as the pass that did nothing and the user who asked twice.

---

## 1. A `max_tokens` round is treated as "the model is done"

**The clearest defect in this pass, and currently unmeasurable.**

`fromSDKMessage` (`brain/llm.go:441`) collapses every stop reason that is not `tool_use`:

```go
if msg.StopReason == anthropic.StopReasonToolUse {
    resp.StopReason = StopToolUse
} else {
    resp.StopReason = StopEndTurn
}
```

`runPass` (`brain/service.go:172`) reads `StopEndTurn` and returns `nil` immediately —
**before** appending the assistant turn — so `resp.Calls` is discarded. The consequences for a
round that ended on `max_tokens` rather than `end_turn`:

- the round is billed in full: the entire conversation re-sent as input, plus up to
  `maxOutputTokens` (4096) of output;
- the tool calls it contains never execute (and the truncated one is unparseable anyway);
- the pass returns success, so the runtime marks the event done — no retry, no dead-letter;
- the user sees nothing happen and re-asks, which costs a **second complete pass**.

`refusal` and `pause_turn` collapse identically. `pause_turn` is not reachable today (no
server tools), `refusal` is rare; `max_tokens` is the live one.

**Why it is plausible rather than theoretical.** Thinking is disabled, so all 4096 tokens are
tool calls and text — but the prompt asks for markdown ticket bodies with "headings, lists,
and emphasis" (`prompt.go:114`) and instructs the model that when the user requests several
features "it may be appropriate to break it to many tickets" (`prompt.go:118`). A round
emitting two or three richly-formatted `create_ticket` calls is the intended behaviour and is
in the right order of magnitude to hit the ceiling.

**Why the 08-05 window did not catch it.** `logRound` (`llm.go:297`) logs
`resp.StopReason` — the value *after* the collapse above:

```go
slog.String("stop_reason", string(resp.StopReason)),
```

So a truncated round is recorded as `stop_reason=end_turn`, indistinguishable from a healthy
one. Every `max_tokens` round in that 2,425-round window is in the data as a clean finish.
This is an instrumentation gap of exactly the same character as the cache-write TTL gap the
previous pass closed with recommendation D, and it wants the same treatment: **measure
first, then decide the handling.**

## 2. No request timeout on the LLM call, and two retry layers that multiply

`newBrainLLM` (`cmd/kiln/wiring.go:407`) builds the client with credentials only:

```go
return brain.NewAdapterWithClient(
    brain.Config{Model: model, Effort: effort},
    option.WithAPIKey(cfg.AnthropicAPIKey),
)
```

No `option.WithRequestTimeout`, and neither `Adapter.Do` nor `Service.HandleEvent` sets a
deadline of its own. The SDK therefore applies its own defaults for a non-streaming call:
`CalculateNonStreamingTimeout` returns **10 minutes**, and `MaxRetries` defaults to **2**
(`anthropic-sdk-go@v1.56.0`). One round can consume ~30 minutes of wall clock before it
returns an error.

Two things make that worse than it sounds:

- **Head-of-line blocking.** The events worker is strictly serial per project (04 §4, and
  `worker.go`'s busy-set claim). A wedged round holds the project's only slot, so the user's
  next message waits behind it — the surface symptom is "Kiln stopped responding", not "that
  one pass was slow".
- **Layered retries.** When the round finally fails, the runtime's own budget re-runs the
  **entire pass** — `MaxAttempts` 8 with backoff to 60s (`worker.go:250`). The SDK's 2
  retries sit *inside* each of those 8. A persistent upstream failure is therefore retried up
  to 24 times, and each outer retry re-pays the whole pass, not the failed round.

A dispatcher round with `effort=medium`, thinking disabled and a 4096-token ceiling has no
legitimate reason to take minutes. This is the smallest diff in the document.

## 3. `get_agent_updates` puts agent output into context with no cap

The same payload is budgeted on one path and unbudgeted on the other:

| Path | Bound |
| --- | --- |
| `agent.turn_completed` → `renderEvent` | `truncateHeadTail(p.Output, AgentOutputTruncateBytes)` — 8000 bytes, head+tail (`service.go:325`) |
| `get_agent_updates` → `formatUpdate` | **none** (`tools.go:1212`) |

`formatUpdate` appends `u.LatestOutput` verbatim, and the value reaching it is
`ReadLatestOutput`'s raw assistant-message content (`agent/amika/client.go:390`) — a coding
agent's final turn message, routinely multi-KB and occasionally far more.

The cost is not one-off. Whatever lands in that tool result is re-sent on **every remaining
round of the pass**, and — because the conversation breakpoint moves to the newest block each
round — cache-*written* as it goes. The 08-05 window logged **130 `get_agent_updates` calls**
across 479 passes, so this is a regularly-walked path, not an edge case.

The `AgentOutputTruncateBytes` rationale applies verbatim here: *the brain judges outcomes, it
does not re-review diffs* (06 §3.3).

## 4. Every done verification spends the full 16KB bash cap on an unbounded `git log`

Two places tell the model how to find the commit that carries a ticket's work, and neither
bounds the output:

- `prompt.go:164` — *run "git fetch origin" first, then inspect "git log origin/main"*
- `tools.go:325` — *find it first with the bash tool (git fetch origin, then git log
  origin/main)*

Bare `git log` emits full commit records — author, date, complete message body — for the
entire history. `repo.capOutput` caps that at `outputCapBytes` = **16KB** and sets
`Truncated` (`repo/repo.go:483`), so on any repo with real history the model receives a
truncated 16KB wall (~4k tokens) in exchange for a single SHA lookup, and it then sits in the
conversation for every subsequent round of the pass.

`-n 20 --oneline` answers the same question in a few hundred bytes. The `bash` tool
description is also where a general output-cap expectation is set, so `outputCapBytes` itself
is worth revisiting: 16KB is sized for a general-purpose shell, not for a dispatcher whose
entire round budget is 4096 tokens.

## 5. Two `git fetch origin` calls per done, back to back

The prompt has the model fetch via `bash`, and then `verifyDoneOnMain` immediately fetches
again itself (`repo/repo.go:264`):

```go
if code, out := run("fetch", "origin"); code != 0 { ... }
```

So an accepted ticket costs two full fetches seconds apart, each under the 30s `runTimeout`,
both inside the model loop with the user waiting.

The second fetch is *not* simply deletable: the gate's correctness rests on verifying against
freshly-fetched refs rather than trusting whatever the model happened to run, and it fails
closed by design. The fix is a short freshness window on the shell's fetch, which is why this
is a ticket and not a one-liner.

## 6. Nothing tells the model it may batch its reads into one round

The previous pass measured **846 leading read calls across 478 passes — 1.77 per pass** before
the first action, with 86% of passes opening on at least one read (§3 there). It framed the
remedy as recommendation E: reverse the CRUD consolidation and re-inject a board snapshot,
explicitly left as a judgement call rather than a proposal.

There is a second, independent remedy that does not touch that decision. The loop **already**
executes multiple tool calls per round — `dispatchAll` (`service.go:201`) iterates
`resp.Calls` — and the API permits parallel tool use by default. But nothing in the system
prompt or the tool descriptions ever tells the model it may ask for several reads at once. The
pass shapes in the 08-05 window are consequently serial:

```
get_ticket -> list_agents -> send_to_agent -> post_update      (34 passes)
get_ticket -> list_agents -> send_to_agent                     (27 passes)
```

Both of those are one round of reads followed by one round of action, spent as two rounds of
reads plus one of action. Each avoided round is a full conversation re-send (~4–6k prompt
tokens) plus its ~4s.

This lands whether or not E is ever taken, and if E *is* taken it still applies to
`get_agent_updates` + `bash`, which no board injection covers.

## 7. Tool calls within one round dispatch strictly serially

`dispatchAll` runs one call at a time and collects results in order. Three of the tools behind
it are network-bound: `list_agents` (an Amika `ListWorkers` round-trip plus a DB read per
worker — see §8), `get_agent_updates` (another Amika round-trip), and `bash` (up to 30s).

Today this is mostly latent, because §6 means the model rarely batches. It becomes the binding
constraint the moment §6 lands, which is why the two belong in one piece of work rather than
sequentially.

Read-only tools (`list_tickets`, `get_ticket`, `search_tickets`, `list_updates`,
`list_agents`, `get_agent_updates`, `bash`) are safe to run concurrently. Mutations must stay
strictly ordered — `applyUpdate`'s field→approval→state sequencing and the board's
preconditions both depend on it — so this is a *partition*, not a blanket parallelization.

## 8. `ListAgents` is N+1 on the DB and runs unconditionally every 10s per project

`agent/service.go:269`, inside the per-worker loop:

```go
for _, w := range live {
    workerID := slotIDForName(prefix, w.Name, slotIDs)
    info := AgentInfo{...}
    if prev, found, lerr := s.store.LatestForWorker(ctx, workerID); lerr == nil && found {
```

One `LatestForWorker` query per worker, on top of the provider's `ListWorkers` HTTP call.
`refreshStatuses` (`service.go:389`) calls it for **every project on a 10s tick**
(`LivenessInterval`), and cannot be gated on demand the way the SSE hub's copy is, because
`SetWorkerHealth` genuinely needs the result whether or not anyone is watching — the pull
binds Ready tickets only to healthy sandboxes (03 §5).

This is not model spend. It earns its place here because it also sits in the request path of
the brain's `list_agents` tool — **237 calls** in the 08-05 window — so it is latency inside
passes, and it scales linearly with project count under 11 §3.

`hub.go:150` already names the cost precisely for its own path:

> *"a board query plus an `agentJoin`, and that join costs a provider round-trip
> (ListWorkers) and one turn lookup per worker"*

## 9. Smaller observations

- **`runtime.Feed()` re-assembles on every `feed.updated`** — a board view plus four
  sequential store reads (`runtime/service.go:329`), re-run for each notification
  post/edit/retract and each board transition. A pass with three `post_update`s costs ~15
  sequential queries. Parallelizable; low value.
- **`renderBoard` / `renderColumn` are now reached only via `formatRoster`.** Board injection
  is gone, so the "render order" comments in `service.go` describe a tool result, not a
  context block. Cosmetic; worth a comment fix if E is ever decided against.
- **`bash` results are capped at 16KB** and re-sent every subsequent round. Related to §4 but
  broader than the `git log` case.

## 10. Checked and clean — do not spend time here

Recorded so the next pass does not re-walk them:

- **One model call site.** `brain/llm.go` is the only caller of the Anthropic SDK in the
  backend. The other `anthropic` hits (`identity/*`, `api/identity_handlers.go`) are
  per-user API-key storage plumbing, not calls. There is no duplicate-call defect to find.
- **Prompt caching is correctly placed** — the 08-05 window measured 86% of prompt tokens
  served from cache, and both breakpoints are doing their job.
- **Event dedup works.** `EnqueueEvent`'s idempotency key makes a crash-replayed agent
  completion a no-op, so a redelivery cannot cause a second brain pass.
- **Tenant bundles and GitHub tokens are both cached** — `tenant.Registry.For` is
  single-flight per project with generation-checked invalidation; `InstallationTokens` caches
  per installation with a refresh margin. Neither re-mints per event.
- **`search_tickets` is tightly bounded** — 5 hits per page, 160-byte body excerpts.
- **`board.Snapshot` is 3 queries, not N+1** — one ticket read plus two counts.
- **`PushBoard` already skips assembly when no client is connected** (`hub.go:161`, landed
  `6abea23`).

---

## Recommended approach

Ordered by (value ÷ risk), like the previous pass.

### A. Log the raw stop reason — **prerequisite, not an optimization**

**Closes §1's instrumentation gap.** Carry `msg.StopReason` into `logRound` alongside the
mapped value, exactly as recommendation D carried the cache-write TTL split alongside the
aggregate. One line, no behaviour change, and it is the only way to learn whether §1 costs 0.1%
of rounds or 3%.

Land this **first**. Everything about §1's actual handling should be decided against a number.

### B. Set a request timeout

**Fixes the §2 tail.** `option.WithRequestTimeout` at 60–90s in `newBrainLLM`, beside the
existing `WithAPIKey`. Immediately reversible, no schema or prompt churn, and it converts an
unbounded stall into a normal handler error that the existing retry path already knows how to
absorb.

The retry-layer reconciliation (SDK `MaxRetries` nested inside the runtime's `MaxAttempts`) is
a separate, larger question — worth its own ticket, and worth deciding after the timeout has
been in place long enough to see how often it fires.

### C. Truncate `get_agent_updates` output

**Fixes §3.** Reuse `truncateHeadTail(out, AgentOutputTruncateBytes)` in `formatUpdate` — the
same helper and the same budget the event path already applies to the same data. The asymmetry
looks like an oversight rather than a decision: 06 §3.3's reasoning covers both paths and
names neither as exempt.

### D. Bound the `git log` guidance

**Fixes §4.** Change both call sites to `git log origin/main -n 20 --oneline`. Prompt changes
are behaviour changes and ride the golden-test gate (06 D7), but the blast radius here is one
sentence in two strings, and the golden decision tests pin the outcome.

Worth pairing with a look at `outputCapBytes` — 16KB per `bash` result is a lot of context for
a dispatcher, and §9 notes it applies well beyond the `git log` case.

### E. Batch the reads — prompt (§6) and dispatch (§7) together

**The largest latency lever that does not reverse an architectural decision.** Two halves that
should ship as one ticket, because either alone is half a fix:

1. A line in the system prompt's "Managing" section telling the model to issue every read it
   needs for a decision as parallel tool calls in a single round.
2. Concurrent dispatch for read-only tools in `dispatchAll`, mutations left strictly ordered.

Do (1) without (2) and the batched reads serialize behind each other's network calls. Do (2)
without (1) and nothing changes, because the model still emits one read per round.

This is genuinely independent of the still-open recommendation E from 08-05 — it applies to
`get_agent_updates` and `bash`, which no board injection would cover — so it does not need
that decision made first.

### F. Batch the per-worker turn lookup

**Fixes §8.** One `WHERE worker_id = ANY($1)` in place of the per-worker `LatestForWorker`.
Contained, but it is a store-layer change with its own test surface, and the payoff is latency
and DB load rather than model spend — so it ranks below the token levers above.

### Not recommended

- **Re-litigating board injection (08-05's recommendation E).** Still the user's call, still
  unaddressed here. Recommendation E above is the orthogonal half and does not prejudge it.
- **Chasing §9's feed re-assembly.** Real, small, and nowhere near the model path.
- **Acting on §1 before A has produced a number.** The failure mode is invisible in every log
  record written to date; picking a handling strategy without knowing the rate is guessing.

---

## Method / reproducing

Source read at `22f48c6`, covering the full model path: `internal/brain` (all of `llm.go`,
`service.go`, `tools.go`, `prompt.go`, `types.go`, `ports.go`, `search.go`), `internal/runtime`
(`service.go`, `worker.go`), `internal/agent` (`service.go`, `amika/client.go`),
`internal/repo`, `internal/steward`, `internal/tenant`, `internal/board/postgres`, and the
`cmd/kiln` wiring. SDK defaults verified against the pinned
`anthropic-sdk-go@v1.56.0` in the module cache (`client.go:297` for the non-streaming timeout,
`internal/requestconfig/requestconfig.go:173` for `MaxRetries`).

**Every count in this document is quoted from the 2026-08-05 window** (479 passes, 2,425
rounds, 2026-08-02T18:15Z → 2026-08-05T11:52Z) and is labelled where used. No new measurement
was taken. That is the main limitation of this pass and the reason recommendation A is
sequenced first: §1 in particular cannot be sized from any log record written before it lands,
and §3's and §4's true token cost needs a window measured after the cache-write TTL split
(shipped 2026-08-05) to be costed exactly rather than as a range.

The join method for a future window is unchanged from the previous document: `brain: llm
round`, `brain.tool` and `runtime.event.*` records, joined on `turn_id`.
