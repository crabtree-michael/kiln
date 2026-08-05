# Brain optimization — findings and proposal

**Date:** 2026-08-05 · **Service:** `srv-d953nmcvikkc73d8aq60` (`kiln`, prod)
**Log window:** 2026-08-02T18:15Z → 2026-08-05T11:52Z (~2.77 days, 17.9k log records)
**Status:** investigation + proposal. Recommendation C (`effort`) has since landed at
`medium`; A, B, D, and E are still unimplemented and left for review.

> The brain is not a separate Render service — it is `internal/brain` inside the single
> `kiln` web service. All figures below are reconstructed from the structured `brain: llm
> round` / `brain.tool` / `runtime.event.*` records, joined on `turn_id`.

---

## 0. Headline

| | |
| --- | --- |
| Brain passes in window | **479** (335 `agent.turn_completed`, 146 `human.message`) |
| LLM rounds | **2,425** — mean 5.06/pass, median 4, p90 8, max 21 |
| Prompt tokens | **25.3M** (86% served from cache) · output 455k |
| Est. LLM spend | **$192–$374 / 30 days** at current volume (range spans intro vs list pricing and the unknown 5m/1h cache-write split) |
| Pass latency | mean 17.0s, median 13.6s, p90 31.5s, max 74.5s |
| Tool-call error rate | **7.7%** (200 of 2,600) |

The brain costs roughly **15–30× the infrastructure it runs on** ($13/mo for the web
service + Postgres). It is the dominant line item, so the levers below are worth taking
seriously — but note that most of the waste is *bounded*: the biggest single defect costs
about 3% of rounds, not 30%. There is no smoking gun, there are four or five small ones.

Prompt caching is working well and is not the problem: 86% of prompt tokens are cache
reads, and the fixed prefix (14 tools + system prompt) is hitting on round 1 at ~4.2k
tokens read against ~2.1k written. The two breakpoints in `llm.go` are doing their job.

---

## 1. `post_update` rejects one in five of its own calls

**The single clearest defect, and the cheapest to fix.**

| Call shape | Count |
| --- | --- |
| `{body, ticket}` → ok | 302 |
| `{text, ticket}` → **error** | 79 |

`post_update` requires `body`; `say` requires `text`. The model reaches for `text` on
**20.7% of `post_update` calls** (79 of 381). Every one of those returns
`invalid tool arguments: post_update requires a non-empty "body" field`, and — checked
across every pass in the window — **100% of them recover** with a corrected `post_update`
later in the same pass. No behaviour is lost. The only cost is a wasted round-trip:
roughly **79 extra rounds (3.3% of all rounds)** and ~4s of latency each.

This is not an unknown failure mode. `tools.go:965` already names it:

> *"the model dropped the field, sent blanks, or used the wrong key — e.g. post_update's
> `body` vs say's `text`"*

The design chose *recovery* (feed the error back, let the model self-correct, per 06 §8)
over *prevention*. The data says the recovery path fires often enough to be worth
preventing instead.

## 2. `update_ticket` fails 39% of the time — but most of it is by design

105 errors on 268 calls. The breakdown matters, because these are not all the same thing:

| Failure | Count | Reading |
| --- | --- | --- |
| `ShapeTicket` on a `working` ticket | 31 | Model edits a ticket an agent is mid-turn on |
| `ShapeTicket` on a `blocked` ticket | 13 | Same shape |
| `MarkReady` / `MarkBlocked` / `AcceptToDone` on a ticket already in that state | 25 | **Working as intended** — 06 §6's "treat `ErrInvalidTransition` as already done" |
| `MarkReady` on `working` / `blocked` | 13 | Model guessing at an unavailable transition |
| Malformed args (`nothing to update`, bad `state`) | 7 | Model sent an empty patch |

The idempotency-driven ones (~25) are the design doing its job and should be left alone.
The **~57 preventable ones** are the model guessing which transition a ticket will accept.
It has to guess, because `get_ticket` tells it the current state but not which transitions
that state permits.

## 3. 86% of passes spend their first round(s) reading the board back

The CRUD consolidation removed board injection so that "a pass spends no tokens on state
it doesn't need" (06 §3, superseding D3). The logs say passes almost always need it:

- **409 of 478 passes (86%)** open with at least one read-only tool call.
- **846 leading read calls** total — a mean of **1.77 per pass** before the first action.
- Reads are 40% of all tool traffic (`get_ticket` 491, `list_agents` 237, `list_tickets`
  234, `get_agent_updates` 130).

The most common pass shape in the window is literally a read prologue:

```
get_ticket -> list_agents -> send_to_agent -> post_update      (34 passes)
get_ticket -> list_agents -> send_to_agent                     (27 passes)
get_ticket -> list_agents -> get_agent_updates -> bash -> ...  (20 passes)
```

Each leading read costs a full round-trip: re-send the whole conversation, ~4–6k prompt
tokens, ~4s wall-clock. **This is the largest latency lever in the system** — but it is
also the one that reverses a deliberate architectural decision, so it needs your call
rather than mine. Honest accounting of the trade: injecting a board snapshot adds ~1–2k
tokens written once and re-read on every subsequent round, against saving a round that
costs ~4–6k read plus its tool result. Roughly break-even to modestly positive on tokens;
clearly positive on latency (~4–8s/pass).

## 4. `effort` is never set, so every pass runs at `high`

`llm.go`'s `MessageNewParams` sets `Model`, `MaxTokens`, `Messages`, `Tools`, `Thinking`,
and `System`. There is **no `output_config.effort`**, so Sonnet 5 defaults to `high` on
every round of every pass.

The brain is a dispatcher with thinking explicitly disabled — the exact profile `effort`
was built to tune down. This is the single cheapest experiment available: one field, no
architectural change, immediately reversible via the existing config plumbing.

## 5. Retries re-run whole passes at full cost

- **29 of 481 events (6%)** needed more than one attempt; one needed **7**.
- 18 passes failed outright; 13 with `anthropic messages.new: context canceled`.

`context canceled` on the LLM call is the deploy-restart signature already root-caused in
[`root-cause-2026-08-03-duplicate-instances.md`](root-cause-2026-08-03-duplicate-instances.md)
and [`root-cause-2026-08-03-render-logs.md`](root-cause-2026-08-03-render-logs.md) §3 —
zero-downtime deploys run two instances, and the failed 15s `srv.Shutdown` kills in-flight
work. **This is not a brain defect and should not be re-diagnosed here**; the fix is
already drafted as [`ticket-draft-sse-shutdown.md`](ticket-draft-sse-shutdown.md). It
belongs in this document only because it shows up as brain cost: a retried event re-runs
the entire pass, so that 6% retry rate is ~6% of LLM spend spent twice.

## 6. Smaller observations

- **Redundant calls are not a problem.** Identical `tool+args_hash` repeated within a pass:
  95 of 2,600 calls (4%), in 11% of passes. Not worth chasing.
- **Read-only passes are not a problem.** Only 7 of 479 passes (2%) took no action at all,
  consuming 1% of prompt tokens. The brain is not waking up for nothing.
- **Cache-write is 40–60% of spend.** 3.5M written vs 21.8M read. This is inherent to a
  bounded tool loop that re-sends a growing conversation, and the incremental breakpoint is
  handling it correctly. But we cannot currently tell how much is 5m-TTL (1.25×) vs 1h-TTL
  (2×) writes, because `logRound` logs only the aggregate `cache_creation_input_tokens`.
  The SDK does expose the split (`Usage.CacheCreation.Ephemeral5mInputTokens` /
  `Ephemeral1hInputTokens`, present in the pinned `anthropic-sdk-go@v1.56.0`). **That
  instrumentation gap is why the cost figure above is a range rather than a number.**
- **The skill doc is stale.** `.claude/skills/orchestrator-brain/SKILL.md` says the default
  model is `claude-haiku-4-5-20251001`; `llm.go:20` says `claude-sonnet-5`. Worth
  correcting so the next investigation doesn't start from the wrong cost model.

---

## Recommended approach

Ordered by (value ÷ risk), not by size. The first three are contained changes with golden
tests already in place to catch regressions; the fourth is an architecture decision.

### A. Make `post_update` accept `text` as an alias for `body` — or rename the field

**Fixes §1.** ~79 wasted rounds (3.3%) and ~5 minutes of cumulative latency per window,
for a few lines in `tools.go`. Two options, and I'd want your preference before scoping:

1. **Accept both keys** — decode `text` into `Body` when `body` is absent. Smallest
   change, zero prompt churn, but leaves two names for one concept in the schema.
2. **Rename `post_update.body` → `text`** — makes the tool set internally consistent
   (`say` and `post_update` both take `text`), which is likely *why* the model keeps
   reaching for it. Larger blast radius: wire schema, golden tests, prompt.

Option 2 treats the cause; option 1 treats the symptom in an afternoon. Either way the
golden decision tests pin the outcome.

**Shipped: option 1.** `PostUpdateInput` now carries a `text` field that `resolvedBody()`
falls back to when `body` is blank; the schema still advertises `body` alone, so the two
names never compete for the model's attention. `TestDispatch_PostUpdate_TextAliasesBody`
pins either key posting the card, `body` winning when both carry text, and the blank-both
case still rejected. `edit_update` keeps `body` as its only key — the log window shows no
wrong-key calls there.

### B. Return allowed transitions from `get_ticket`

**Fixes the preventable ~57 of §2.** Have `get_ticket` (and the `list_tickets` rows)
include the transitions the ticket's current state actually permits, so the model stops
guessing. This keeps the board's preconditions authoritative — it just stops making the
model discover them by trial. The ~25 idempotency errors stay exactly as they are, because
06 §6 depends on them.

### C. Set `output_config.effort` explicitly, and sweep it — **shipped at `medium`**

**Addresses §4.** Add `effort` to `brain.Config`, resolved at the composition root
alongside `Model` (backend-only, same as the model — not user-configurable). Start at
`medium`, measure against the live eval set, try `low`. Sonnet 5 at `medium` is roughly
Sonnet 4.6 at `high`, so there is real headroom here for a dispatcher.

**Do this one first.** It is the smallest diff in the list, needs no schema or prompt
change, and its effect is measurable in a day from the existing `brain: llm round`
records.

> **Landed 2026-08-05.** `brain.Config.Effort` + `DefaultEffort = medium`, resolved at the
> composition root from `KILN_BRAIN_EFFORT` (`llm.go`, `cmd/kiln`). The further sweep to
> `low` is deliberately not taken yet — medium is the accepted setting; the env var makes
> the sweep a config change when there is a baseline to measure it against.

### D. Log the cache-write TTL split (prerequisite, not an optimization)

**Closes the §6 instrumentation gap.** Two extra `slog.Int64` attrs in `logRound` from
`Usage.CacheCreation`. This costs nothing and turns the cost estimate from a ±50% range
into a number — which is what any of the above needs in order to be *evaluated* rather
than merely shipped. I'd land this before or alongside A–C so there is a clean baseline.

### E. Revisit board injection — your call, not a recommendation

**Addresses §3, the largest latency lever.** The data (86% of passes read the board before
acting, 1.77 leading reads per pass) is a genuine argument that the CRUD consolidation's
"pass spends no tokens on state it doesn't need" premise does not hold in production. A
compact board snapshot in the round-1 context block would remove ~1–2 rounds from most
passes.

But this reverses a decision made deliberately, and the token math is closer to break-even
than the latency math — so I am flagging it rather than proposing it. If you want it
explored, the honest version is a spike: inject a minimal snapshot (ticket id, title,
state, assignee only — not full bodies), measure rounds-per-pass and tokens-per-pass
against this baseline, and decide on numbers. `list_tickets`/`get_ticket` stay in the tool
set regardless, for the detail the snapshot omits.

### Not recommended

- **Chasing redundant calls** (4% of traffic) or **read-only passes** (2% of passes) —
  both are already small enough to be noise.
- **Re-diagnosing the `context canceled` retries** — already root-caused and drafted
  elsewhere (§5). Landing that ticket reclaims ~6% of brain spend as a side effect.
- **Switching models to reduce cost** — `effort` (C) is the cheaper and more reversible
  knob, and should be exhausted first.

---

## Method / reproducing

Logs pulled via the Render API (`GET /v1/logs`, paginated backwards, `ownerId`
`tea-d94pie5ckfvc73adqv30`) and reconstructed into passes by joining `brain: llm round`,
`brain.tool`, and `runtime.event.*` records on `turn_id`. Cost derived from the per-round
`input_tokens` / `cache_read_input_tokens` / `cache_creation_input_tokens` /
`output_tokens` attributes against Sonnet 5 rates (cache read 0.1×, 5m write 1.25×, 1h
write 2×).

**One caveat on the numbers:** `brain.tool` records truncate `result` at 512 bytes
(`toolResultSummaryBytes`), so the logs do not reveal true tool-result payload sizes. All
token figures above come from the `usage` attributes, which are ground truth; no claim here
rests on the truncated result text.
