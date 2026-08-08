# Brain optimization — sizing the second pass against production

**Date:** 2026-08-08 · **Service:** `srv-d953nmcvikkc73d8aq60` (`kiln`, prod)
**Validates:** [`brain-optimization-2026-08-08.md`](brain-optimization-2026-08-08.md) §1–§8
**Log window:** 2026-08-01T14:55Z → 2026-08-08T11:29Z (164.6 h / 6.9 days — full Render retention)
**Sample:** 4,701 `brain: llm round` · 5,240 `brain.tool` · 1,159 `runtime.event.received` · 666 `repo.shell.run`
**Status:** measurement only. No code changed, nothing implemented.

> **This is the work [`brain-optimization-2026-08-08.md`](brain-optimization-2026-08-08.md) asked for.**
> That pass was a source read and said so plainly: *"nothing new is quantified, because nothing new
> has been measured yet. Sizing these is the first piece of work, not the last."* This document is
> that sizing, for all eight of its findings, against the full retention window.
>
> **Four of the eight do not survive.** §1, §2, §3 and §4 are real defects in code that essentially
> never fire in production. §5 and §6 are confirmed, §7's premise is wrong in a way that helps, and
> §8's cost is real but on a different path than the one it names. Two things the source read could
> not see turned out to matter more than five of the eight (§10, §11).

---

## 0. Headline

| § | Finding (08-08 doc) | Verdict | Production measurement |
| --- | --- | --- | --- |
| 1 | `max_tokens` collapsed to `end_turn` | **Refuted — negligible** | **1 of 4,701 rounds** (0.02%) |
| 2 | No request timeout; 30-min tail; layered retries | **Refuted as a live issue** | **0 of 3,750** round gaps > 30 s |
| 3 | `get_agent_updates` uncapped | **Refuted as a cost driver** | **0.94×** per round once pass length is controlled |
| 4 | Unbounded `git log` burns the 16 KB cap | **Refuted** | **3 of 666** shell runs truncated (0.45%) |
| 5 | Two `git fetch` per done | **Confirmed — small** | 171 double-fetches, ~1/h |
| 6 | Reads not batched into one round | **Confirmed — largest lever** | **11–14% of all rounds** are collapsible |
| 7 | Serial dispatch within a round | **Confirmed latent; premise wrong** | Model already batches in **22.1%** of rounds |
| 8 | `ListAgents` N+1 | **Reframed** | Brain path 2.2/h; the unlogged tick is ~160× bigger |
| **10** | *(new)* Redundant identical tool calls | **Confirmed** | **215 wasted calls, 4.1%**, in 9.3% of passes |
| **11** | *(new)* `update_ticket` illegal-transition churn | **Confirmed, partly fixed** | 39.5% → **10.3%** error rate after fix B |

**The single most useful number in this document is not a finding.** It is the cost *shape*:
**half of all spend is cache writes**, because every round rewrites the growing conversation prefix
at a 25% premium. That makes **rounds-per-pass the dominant cost variable**, which promotes §6 and
demotes every finding about the size of an individual payload (§3, §4).

---

## 1. Cost baseline and its shape

| Line item | Tokens | $ / 164.6 h | Share |
| --- | ---: | ---: | ---: |
| **Cache writes (5m)** | 6,845,757 | **25.67** | **50.1%** |
| Output | 864,322 | 12.96 | 25.3% |
| Cache reads | 41,313,130 | 12.39 | 24.2% |
| Cache writes (1h) | 36,090 | 0.22 | 0.4% |
| Uncached input | 13,532 | 0.04 | 0.1% |
| **Total** | | **51.29** | |

**Stable unit costs: $0.0109 per round, $0.0539 per pass.** These barely move across the window
(pre-fix $0.0109/round, post-fix $0.0112/round), so they are the figures to plan against.

**Do not plan against a 30-day projection.** Extrapolating the window average gives $224/30 d, but
daily tool-call volume ranged from **1,293 (08-03) down to 151 (08-07)** — an 8× swing driven by how
much the project's owner was working, not by anything in the brain. The same extrapolation run on
the two halves separately yields $315/30 d and $96/30 d. The 08-05 doc's $192–$374 range holds; it
cannot usefully be narrowed further at this volume.

The shape is the actionable part. Cache reads are 41.3 M tokens against 13.5 k uncached input —
caching is working almost perfectly, exactly as 08-05 §0 found. But cache *writes* are the largest
line item, and they scale with the number of rounds, not with the size of any one payload. Every
extra round in a pass re-writes the whole prefix.

---

## 2. §1 — `max_tokens` rounds: real, and it fires about once a week

The 08-08 doc calls this "the clearest defect in this pass" and "currently unmeasurable", because
`logRound` (`llm.go:297`) records the value *after* the collapse, so a truncated round is stored as
`stop_reason=end_turn`.

**It is measurable.** `logRound` also records `output_tokens`, and a round stopped at the ceiling
must sit at `maxOutputTokens` = 4096 (`llm.go:58`). That is a signature the collapse cannot hide.

| | |
| --- | --- |
| Rounds at/over the 4096 cap | **1 of 4,701 (0.02%)** |
| Rounds within 5% of the cap | 0 |
| Output tokens p50 / p95 / max | **134 / 538 / 4096** |
| `stop_reason` as logged | `tool_use` 3,731 · `end_turn` 970 |

p95 output is **13% of the ceiling**. The one round that hit it (`evt-1605`, 2026-08-02T16:15) had
an **empty `tool_calls` field** — so it was text generation, and no tool calls were silently
discarded. The predicted failure (a round emitting two or three richly-formatted `create_ticket`
calls, truncated and thrown away) did not occur once in 6.9 days.

The defect is real and the reasoning was sound. Its rate is ~1/week and its observed instance was
harmless. It does not need its own ticket; it needs a cheap guard if the traffic mix ever changes.

---

## 3. §2 — no request timeout: nothing is anywhere near it

| | |
| --- | --- |
| Inter-round gaps measured | 3,750 |
| p50 / p90 / p99 / **max** | 3.3 s / 6.9 s / 12.9 s / **27.6 s** |
| Gaps > 30 s | **0** |
| Pass wall-clock p50 / p90 / p99 / **max** | 14.3 s / 32.7 s / 60.8 s / **92.1 s** |
| Passes > 120 s | **0** (140 over 30 s, 12 over 60 s) |

The SDK's 10-minute non-streaming timeout and the 8 × 2 = 24-retry multiplication are both real in
code and both entirely unexercised. The worst round in the window took 27.6 seconds against a
600-second ceiling — a 22× margin. Head-of-line blocking of a project's queue slot never happened,
and there were no dead-letters.

Total LLM round latency across the window is **4.1 hours**, at a mean of 3.90 s/round; a mean pass
spends ~15.4 s of its ~17 s inside model calls. Latency is round *count*, not round duration —
again pointing at §6.

Still worth adding `option.WithRequestTimeout` — it is a one-line guard against a tail that would be
invisible until it hurt. But it should be reclassified from "largest latency-shaped defect" to
insurance.

---

## 4. §3 — `get_agent_updates`: the effect is pass-length confounding

Raw, the finding looks strongly confirmed:

| | prompt tokens / pass |
| --- | --- |
| Passes calling `get_agent_updates` (180, 18.9%) | p50 **51,213** · mean **76,519** |
| Passes not calling it (771) | p50 **35,263** · mean **45,031** |

A 70% mean difference. **It disappears under control.** Passes that call `get_agent_updates` are
simply longer — **6.84 rounds vs 4.53** — and more tool-heavy (8.17 vs 4.43 calls). Per *round*:

| | $/round | rounds/pass |
| --- | ---: | ---: |
| With `get_agent_updates` | **0.0101** | 6.84 |
| Without | **0.0107** | 4.53 |
| Ratio | **0.94×** | |

Matched pass-for-pass on round count, the ratio stays at or below parity for every bucket with a
usable sample: 4 rounds **0.77×**, 5 rounds **0.83×**, 6 rounds **0.88×**, 7 rounds 1.06×, 8 rounds
0.89×. `get_agent_updates` is a **marker** of a complex pass, not a driver of its cost.

**No tail either.** The largest single-round prompt in the window is 36,662 tokens (a `gau` pass) —
but non-`gau` passes reach 31,463, and the largest cache-creation event (11,906 tokens, new content
entering context) is **not** a `gau` pass. Overall prompt sizes: p50 8,169, p95 18,724, p99 23,397.
There is no evidence of a multi-KB agent output blowing up a context.

The uncap at `tools.go:1212` is real and the asymmetry against `renderEvent`'s 8000-byte budget is
real. Production agent outputs have simply never been large enough to exercise it. Cheap insurance;
not a lever.

---

## 5. §4 — the model already bounds its `git log`

| | |
| --- | --- |
| `git log` calls via `bash` | 447 |
| **Bounded** (`--oneline` / `-n N`) | **446** |
| Unbounded | **1** — and it pipes to `grep`, so its output is small |
| Shell output bytes p50 / p95 / max | **605 / 4,381 / 16,384** |
| Runs hitting the 16 KB cap (`truncated`) | **3 of 666 (0.45%)** |
| Runs over 8 KB | 14 |

The predicted behaviour — "on any repo with real history the model receives a truncated 16 KB wall
in exchange for a single SHA lookup" — happened three times in 6.9 days. The prompt guidance at
`prompt.go:164` and `tools.go:325` does not name a bound, but the model supplies one anyway in
99.8% of calls. `outputCapBytes` may still be worth revisiting on principle; there is no measured
cost to reclaim.

---

## 6. §5 — two fetches per done: confirmed, ~1/h

| | |
| --- | --- |
| Model-issued `git fetch` via `bash` | 365 calls across 325 passes |
| `repo.shell.verify` (each runs its own `VerifyOnMain` fetch) | **171** |
| `repo.shell.verify_pr` | 0 |
| Verifications returning `on_main=true` | 171 of 171 |

Confirmed exactly as described: 171 accepted-done verifications, each adding a second `git fetch`
seconds after the model's own, inside the model loop with the user waiting. That is **~1 redundant
fetch per hour**, on the ~18% of passes that accept a done.

**Measurement gap:** `repo.shell.run` logs `exit_code`, `timed_out`, `truncated` and `output_bytes`
but **no duration**, so the latency cost of a fetch is not observable. Nothing timed out (0 of 666
runs hit the 30 s `runTimeout`), which bounds it, but the actual seconds remain a code-read
inference. The freshness-window fix stays correct and stays small.

---

## 7. §6 — read batching: the one finding worth its own ticket

| | full window | pre-fix | post-fix |
| --- | ---: | ---: | ---: |
| Consecutive all-read rounds beyond the first | **631** | 539 | 92 |
| As share of all rounds | **13.4%** | 13.9% | 11.3% |
| Leading read calls per pass (mean) | **2.16** | | |
| Passes opening with ≥1 read | **84%** | | |
| Rounds per pass (mean) | 4.94 | 5.01 | 4.67 |

The 08-05 doc measured 1.77 leading reads across 478 passes; it is now **2.16** across 951. The
pattern is stable across the fix boundary and across an 8× volume swing, which is what makes it
worth acting on: **11–14% of every round the brain runs is a read round that could have been folded
into the one before it.**

At $0.0109/round that is ~$6.88 per window. The more useful framing is the cost shape from §1: each
avoided round removes a full cache *write* of the conversation prefix, which is the 50% line item —
so the saving is weighted toward the most expensive thing the brain does, not the cheapest.

This lands independently of recommendation E (re-inject the board snapshot), as the 08-08 doc says,
and it still applies to `get_agent_updates` + `bash`, which no board injection covers.

---

## 8. §7 — the model already batches, unprompted

The 08-08 doc's premise is that *"nothing in the system prompt or the tool descriptions ever tells
the model it may ask for several reads at once"*, and that pass shapes are *"consequently serial"*.
The first half is true. The second is not.

| Calls in one round | Rounds |
| ---: | ---: |
| 0 | 969 |
| 1 | 2,691 |
| **2** | **974** |
| 3 | 56 |
| 4 | 5 |
| 5 | 5 |
| 6 | 1 |

**22.1% of rounds already carry more than one tool call**, up to six. Parallel tool use is working
and is already partly exercised without any prompt telling the model to use it.

This *strengthens* §7 rather than weakening it. `dispatchAll` (`service.go:201`) runs those calls
strictly serially today, so the concurrency cost is already being paid on a fifth of all rounds, not
"mostly latent". And it raises confidence in §6: the behaviour being asked for is one the model
demonstrably already has, so prompting for it is a nudge rather than a new capability.

The two belong in one piece of work, as the doc says. The read/mutation partition it specifies
(`list_tickets`, `get_ticket`, `search_tickets`, `list_updates`, `list_agents`,
`get_agent_updates`, `bash` concurrent; mutations strictly ordered) is unchanged by anything here.

---

## 9. §8 — right cost, wrong path

| Path | Frequency |
| --- | --- |
| Brain `list_agents` tool | **369 calls / 6.9 d = 2.2 per hour** |
| `refreshStatuses` liveness tick | ~360 per hour per project (`LivenessInterval` = 10 s) |

The N+1 is real (`agent/service.go:269`, one `LatestForWorker` per worker on top of a `ListWorkers`
HTTP call). But the brain's in-pass usage is **~160× smaller** than the background tick, so framing
it as "latency inside passes" points at the wrong 0.6% of the traffic. It is a background-load and
provider-round-trip issue that scales with project count under 11 §3.

**Measurement gap, and it is a blocker:** `refreshStatuses` emits **no log line at all**. The 360/h
figure is derived from `LivenessInterval` and project count, not observed — I could not measure the
tick, its duration, or its DB cost from logs. Instrumenting it is a prerequisite to sizing the fix,
in the same way §5 of the part-2 root-cause doc needed one log line before the rebuild loop could be
explained.

---

## 10. New — redundant identical tool calls (4.1% of all calls)

`brain.tool` logs an `args_hash`, which makes exact-duplicate calls directly countable. Within a
single pass:

| | |
| --- | --- |
| Passes with ≥1 repeated identical call | **99 of 1,070 (9.3%)** |
| Wasted duplicate calls | **215 of 5,240 (4.1%)** |

| Tool | Duplicated calls |
| --- | ---: |
| `get_ticket` | 61 |
| `get_agent_updates` | 52 |
| `list_tickets` | 39 |
| `list_agents` | 36 |
| `update_ticket` | 14 |
| `bash` | 7 |
| `delete_ticket` | 5 |
| `list_updates` | 1 |

The model re-reads state it already has in context — `evt-2385` called `get_ticket` on the same id
**four times** and `get_agent_updates` three times in one pass. This is the ticket's "redundant model
calls" in its most literal form, and it is not among the eight findings.

It also compounds §6: a duplicate read is both a wasted call *and*, usually, a wasted round.

**Caveat:** a re-processed event keeps its `turn_id` across instances, so a replayed pass inflates
this count. Replays are rare (3 events in a 70 h sub-window, per the separate root-cause series), so
they cannot account for 99 passes — but the true figure is slightly below 4.1%.

---

## 11. New — `update_ticket` illegal-transition churn, and what fix B actually bought

`update_ticket` is the highest-error tool in the window: **220 errors of 636 calls (34.6%)**,
overwhelmingly one message:

```
board: mutate ticket <id>: board/postgres: tx: board: cannot ShapeTicket a ticket in state "working"
```

The model repeatedly tries to edit fields on a ticket that is already `working`, and the errors
cluster on individual tickets (21, 12, 10, 8, 7 errors on single ids) — it retries the same illegal
edit rather than backing off.

Recommendation **B** ("allowed transitions", `d0535bea`, deployed 2026-08-05T14:32Z) targets exactly
this, and the window straddles it:

| | before | after |
| --- | ---: | ---: |
| `update_ticket` calls | 529 | 107 |
| Errors | 209 (**39.5%**) | 11 (**10.3%**) |
| — of which illegal transition | 193 | 11 |

**B worked — a 4× reduction — and a 10.3% residual remains.** Every one of those is a wasted
round-trip and, per 08-05 §1's reasoning about `post_update`, usually a wasted round.

### The shipped A–D recommendations, validated

| Fix | Evidence |
| --- | --- |
| **A** — `post_update` accepts `text` (`175f14dc`, 08-05T14:24Z) | `"text"` key used in **102 of 604** calls before (16.9%), **100 errored**; after, **0 of 5** used it and none errored. *Small post-fix sample (5 calls) — the mechanism is confirmed, the rate is not.* |
| **B** — allowed transitions (`d0535bea`, 08-05T14:32Z) | 39.5% → 10.3% `update_ticket` error rate (above) |
| **C** — `effort=medium` | In force; p95 output 538 tokens |
| **D** — cache-write TTL logging (`2ec57c90`, 08-05T21:27Z) | Split fields present on **785 rounds**; this document's §1 cost split is only possible because of it |

**Combined, the overall tool-call error rate fell from 7.82% to 1.90%** — a 76% reduction against
the 08-05 baseline of 7.7%. That is the clearest evidence in this document that the previous pass's
cheap fixes were the right call.

*Confounder, stated plainly:* the before/after halves also differ in volume (1,293 vs 151 tool
calls/day at the extremes) and in what the owner was working on. The attribution rests on the error
*classes* that A and B specifically target falling to near-zero, not on the aggregate alone.

---

## 12. Revised priorities

1. **§6 + §7 as one ticket.** The only finding with a measured double-digit share of rounds
   (11–14%), amplified by the cost shape in §1 — each avoided round removes a cache-prefix rewrite,
   the 50% line item. §7 is already load-bearing at 22.1% of rounds, not latent.
2. **§10 + §11 (both new).** 4.1% of calls are exact duplicates and 10.3% of `update_ticket` calls
   still fail on an illegal transition. Together these are a larger measured waste than §1–§4
   combined, and §11 has a proven remedy shape — B already cut it 4×.
3. **§5.** Real, ~1/h, small; take the freshness window when someone is next in that code.
4. **§2.** Keep as a one-line `WithRequestTimeout` guard. Reclassify from "largest latency defect"
   to insurance.
5. **Instrument `refreshStatuses`** before sizing §8 — it is currently unobservable.
6. **§1, §3, §4 — do not open tickets.** 0.02%, 0.94× and 0.45% respectively. All three are genuine
   code defects; none is worth a ticket at this traffic. §1 and §3 deserve a cheap guard if the
   payload mix ever changes; §4 needs nothing.

---

## 13. Method and caveats

Render `/v1/logs` for `srv-d953nmcvikkc73d8aq60`, owner `tea-d94pie5ckfvc73adqv30`, paged backwards
via `nextEndTime`, one pull per exact `msg` value. The API rate-limits aggressively on sustained
paging (HTTP 429); calls were spaced ~1.5 s apart with `Retry-After` honoured. Passes are joined on
`turn_id` (`evt-<event_id>`), matching the method of the 08-05 doc and the root-cause series, so the
windows are directly comparable.

Costs use Sonnet 5 list rates ($3/$15 per Mtok, cache write 5m $3.75 / 1h $6.00, cache read $0.30).
`logRound`'s 5m/1h split fields landed mid-window (recommendation D, `2ec57c90`); the 3,916 rounds
written before it carry only the aggregate `cache_creation_input_tokens`, which is billed here at
the 5m rate. If some of that was 1h-TTL, the true cost is slightly higher than $51.29.

Bounding conditions:

- **7-day retention** caps every count. Nothing before 2026-08-01T14:55Z is knowable.
- **One active project, one user.** §8 in particular scales with project count and cannot be
  extrapolated from this window.
- **Volume varied 8×** across the window. Per-round and per-pass costs are stable and should be
  planned against; 30-day projections should not.
- **§3 and §1 are payload-shape-dependent.** Both could re-rate under heavier or differently
  formatted agent output. Their refutation is "does not occur at this traffic mix", not "cannot
  occur".
- **Two things could not be measured at all:** `refreshStatuses`'s liveness tick (no log line, §9)
  and shell command duration (`repo.shell.run` logs no timing, §6). Both are named as gaps rather
  than estimated.
- Counts are of log lines, not incidents. Where a claim rests on code rather than logs it says so.
