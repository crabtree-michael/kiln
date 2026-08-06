# Ticket draft — DeepSeek as a second brain provider, and a per-project model selector

Drafted 2026-08-06 as a **research / proposal** deliverable. Reviewed and **decided
2026-08-06** — see §6.1. Phase 0 (the spike) is greenlit; phases 1–3 remain gated on its
result. No implementation in this document.

Sources for every external claim are footnoted in §8. Everything about Kiln's own code is
cited by file:line against `main` at `fa97ae5`.

Paste the title/body below into the board; the rest is working detail.

---

## Title

Evaluate DeepSeek as an alternative brain provider, behind a per-project model selector

## Body

DeepSeek ships an **Anthropic-compatible endpoint** (`https://api.deepseek.com/anthropic`),
which means the brain's existing `Adapter` (`backend/internal/brain/llm.go:175`) can in
principle be pointed at it with a base-URL and API-key override and nothing else. Cost at
Kiln's measured volume drops **~15–40×** (§2). But DeepSeek's V4 models are always-thinking
reasoning models, and three of Kiln's load-bearing assumptions — thinking disabled, tool
errors flagged with `is_error`, and cache-write accounting — are each either ignored or
undocumented on that endpoint (§3). At least one of those (thinking-block replay across a
multi-round tool loop) has a plausible path to breaking **every pass after round 1**.

**Decision (2026-08-06): the ~1-day spike in §5 phase 0 is greenlit, with the kill criteria
as written.** Nothing in phases 1–3 starts until it passes. DeepSeek API credentials are
being provisioned. The selector, if we get that far, is **per-project and gated** — see
§6.1.

---

## 1. What DeepSeek's API offers

| | `deepseek-v4-flash` | `deepseek-v4-pro` |
| --- | --- | --- |
| Context window | 1M | 1M |
| Max output | 384K | 384K |
| Tool calls | ✅ | ✅ |
| JSON output | ✅ | ✅ |
| Anthropic-format API | ✅ | ✅ |
| Thinking | on by default | on by default |
| Input, cache hit | $0.0028 / M | $0.003625 / M |
| Input, cache miss | $0.14 / M | $0.435 / M |
| Output | $0.28 / M | $0.87 / M |

Three surfaces are offered: an OpenAI-compatible one at `https://api.deepseek.com`, a
Responses API (flash only), and an **Anthropic-compatible one at
`https://api.deepseek.com/anthropic`**. The last is the one that matters to us.

**Concurrency, not rate limits.** DeepSeek publishes account-level *concurrency* caps rather
than TPM/RPM: 2,500 concurrent for flash, 500 for pro, with HTTP 429 above that and free
capacity expansion on request. A request occupies a slot from send until the response
completes. Kiln runs one brain pass per event with a mean pass latency of 17.0 s
(`docs/brain-optimization-2026-08-05.md` §0) — we are nowhere near either cap, and the
`metadata.user_id` parameter (which the Anthropic-compat endpoint does support) would give
per-project isolation if we ever were. Connections idle >10 min without starting inference
are closed server-side; keep-alives are sent during processing.

**Caching is automatic and unpriced-as-a-write.** DeepSeek does prefix caching with no
`cache_control` markers, no minimum prefix documented, best-effort hit rate, and eviction
"within a few hours to a few days". Crucially there is **no cache-write premium** — a miss is
just an input token at the normal rate. That inverts the economics we optimized for in
`docs/brain-optimization-2026-08-05.md` §6, where cache writes are 40–60% of spend.

**Pricing warning, verbatim from the docs:** *"We plan to raise the overall pricing for
DeepSeek API services in the near future, with a significant increase expected."* Any cost
case below has an unknown shelf life.

## 2. Cost, against our own measured volume

Scaling the 2.77-day window in `docs/brain-optimization-2026-08-05.md` §0/§6 to 30 days
(×10.83): **236M cache-read prompt tokens, 37.9M cache-write, 4.9M output.**

| Provider / model | 30-day brain spend | vs today |
| --- | --- | --- |
| Anthropic `claude-sonnet-5` (today, list price) | **$287 – $372** | — |
| `deepseek-v4-pro` | **~$22** | ~15× cheaper |
| `deepseek-v4-flash` | **~$7** | ~45× cheaper |
| `deepseek-v4-pro`, *zero* cache hits | ~$124 | ~2.6× cheaper |
| `deepseek-v4-flash`, *zero* cache hits | ~$40 | ~8× cheaper |

The zero-cache rows are the honest floor: DeepSeek's caching is best-effort and we lose the
explicit breakpoint control that `llm.go:198-220` currently exercises. **Even assuming the
cache never hits at all, DeepSeek is cheaper than today's Anthropic bill.** The cost case
does not depend on caching working, which is what makes it robust.

(My Anthropic figure of $287–$372 reconstructs the doc's $192–$374 range at list rather than
intro pricing — the arithmetic agrees, which is the check that the token volumes are being
applied correctly.)

For scale: the brain is currently "15–30× the infrastructure it runs on" ($13/mo). On
`deepseek-v4-flash` it would be roughly *half* the infrastructure cost.

## 3. Does it drop into Kiln's brain architecture?

**The module boundary is already right.** `brain.LLM` (`ports`-style interface at
`llm.go:164`) is a one-method port; the golden decision tests run against a scripted fake and
never touch a network (`06` §9). Nothing in `service.go` or `tools.go` is Anthropic-aware. A
second provider is a second `LLM` implementation plus composition-root wiring — the
architecture anticipated this.

**And the Anthropic-compat endpoint makes even that mostly unnecessary.** `NewAdapterWithClient`
(`llm.go:192`) already takes `option.RequestOption`s. Adding `option.WithBaseURL(...)` alongside
the existing `option.WithAPIKey(...)` in `newBrainLLM` (`wiring.go:389`) is, mechanically, a
four-line change. Model names pass through directly (`deepseek-v4-pro` is accepted verbatim;
`claude-*` names silently map, which we should avoid relying on).

What survives unchanged:

| Kiln behavior | Status on DeepSeek |
| --- | --- |
| 14-tool schema (`name`/`description`/`input_schema`) | ✅ supported |
| `system` prompt block | ✅ supported |
| `tool_use` / `tool_result` round-tripping | ✅ supported |
| `max_tokens`, `stop_sequences`, `stream` | ✅ supported |
| `output_config.effort` (we set `medium`) | ✅ supported — `effort` is the one field honored |
| `tool_choice` (we never set it) | n/a — see §4 |
| Golden decision tests | ✅ unaffected — scripted fake, no network |

## 4. Gaps and limitations

Ordered by how badly each would hurt.

### 4.1 Thinking blocks and the multi-round tool loop — the headline risk

DeepSeek V4 models are **always in thinking mode**; the docs list `thinking` as only
partially supported (`budget_tokens` ignored) and are **silent on whether
`{"type": "disabled"}` is honored**. Kiln sets exactly that, deliberately
(`llm.go:236`), because `MaxTokens` caps thinking + tool calls + text together and
`maxOutputTokens` is only **4096** (`llm.go:58`).

Two failure modes follow, and they compound:

1. **Truncation.** If thinking runs regardless, a reasoning model's CoT plus tool calls has
   to fit in 4096 tokens. A round that spends the budget thinking returns truncated or
   missing tool calls.
2. **Replay rejection.** `fromSDKMessage` (`llm.go:427`) maps *only* `TextBlock` and
   `ToolUseBlock` — thinking blocks are **dropped on the floor** — and `toSDKMessages`
   (`llm.go:351`) rebuilds each assistant turn from `Text` + `Calls` alone. If DeepSeek
   requires prior reasoning content to be replayed on subsequent turns, every round after
   the first in every pass fails. This is not hypothetical: the equivalent bug on DeepSeek's
   OpenAI-compat path is a filed, reproduced issue — a 400 reading *"The reasoning_content in
   the thinking mode must be passed back to the API"*, triggered specifically by multi-turn
   tool-call flows of 3+ calls, with single-turn flows unaffected.[^1] Kiln's mean pass is
   **5.06 rounds** with a p90 of 8 and a max of 21.

   Whether the Anthropic-compat endpoint has the same requirement is **unverified**. It is
   the single most important thing the spike must answer.

### 4.2 Tool calls emitted as prose

An open, unresolved DeepSeek issue reports V4-Pro intermittently writing the function name
and JSON arguments into the message **content** instead of a structured tool-call block —
~11% of completions in the reported session, non-deterministic, no official response, no
workaround.[^2] For Kiln this is silent: `fromSDKMessage` sees text and no `tool_use`, so
`StopReason` resolves to `StopEndTurn` (`llm.go:442`) and the pass **ends having done
nothing**, with the phantom call text going out as the assistant's turn. No error, no retry,
no dead-letter. That failure is materially worse than a 400.

### 4.3 `is_error` on tool results is ignored

The compat table lists `tool_result.is_error` as **ignored**. Kiln feeds board errors back
verbatim and the idempotency rule (`06` §6) depends on the model reading
`ErrInvalidTransition` as "already done, never retry" — see `ports.go:14-16` and the
`//nolint:wrapcheck` on `applyState`. The *text* still reaches the model, so this is
degraded rather than broken, but the "this was an error" signal is gone. Given our tool-call
error rate is 7.7% (`brain-optimization` §0), this is a real accuracy risk and one the
golden tests cannot catch (they never call the real endpoint).

### 4.4 Cost observability goes dark

`logRound` (`llm.go:289`) emits five Anthropic-specific usage fields, including the
5m/1h cache-write split that was added *specifically* so brain spend could be costed as a
number instead of a range (`brain-optimization` §6/D). `cache_control` is ignored on
DeepSeek, so those fields will be zero or absent; DeepSeek reports
`prompt_cache_hit_tokens` / `prompt_cache_miss_tokens` instead, which the Anthropic response
shape has nowhere to put. **We would lose the instrumentation we just built.** Any DeepSeek
adapter needs its own usage logging or we are flying blind on the thing the whole exercise
is about.

### 4.5 Smaller items

- **`cache_control` ignored** — no error, just no effect. Our two carefully-placed
  breakpoints become no-ops; DeepSeek's automatic caching substitutes for them.
- **`anthropic-version` / `anthropic-beta` headers ignored** — the SDK sends them; harmless.
- **`tool_choice` restrictions** — V4 reportedly rejects `required` and named-tool
  `tool_choice` with a 400 while in thinking mode.[^3] **Kiln never sets `tool_choice`**
  (`llm.go:225-242`), so this does not affect us. Noted so it does not resurface as a
  surprise if someone later adds it.
- **Images / documents / server tools / MCP unsupported** — Kiln's brain uses none of them.
  `post_update` carries an `image_url` but that is a feed field, not a content block.
- **Latency.** An always-thinking model over a 5-round mean pass will be slower than
  Sonnet-with-thinking-off. `06` §2 chose the current profile explicitly because "latency
  matters doubly when voice arrives" — and voice has since shipped (`09`). The spike must
  measure this, not assume it.

## 5. The plan

### Phase 0 — spike ✅ **greenlit 2026-08-06** — this is the ready work

A throwaway Go program against the live endpoint, reusing `brain`'s real tool schema
(`toSDKTools`) and system prompt. Not committed to `main`; findings written up in this doc.

Needs a `DEEPSEEK_API_KEY` (being provisioned). Budget ~1 day. Because it is throwaway and
never imports into `main`, it does not ride the `make check` hard gate — but the write-up
does need to land in this file, and it is the deliverable that closes the spike.

Answer, in order:

1. Does `thinking: {type: disabled}` get honored, error, or get silently ignored?
2. Does a **≥5-round** tool loop survive round 2+ **when thinking blocks are dropped from the
   replayed assistant turns**, exactly as `toSDKMessages` does today? (§4.1 — the gate.)
3. What `stop_reason` values come back, and does `tool_use` map cleanly onto `StopToolUse`?
4. Over ~50 rounds of realistic traffic, how often does a tool call arrive as prose? (§4.2)
5. What does `usage` actually contain? (§4.4)
6. Per-round and per-pass latency, against our p50 13.6 s / p90 31.5 s baseline.

**Kill criteria — if any of these hold, stop and write it up:** (2) fails and cannot be
fixed by threading thinking blocks through `LLMMessage`; (4) exceeds ~2%; or p90 pass latency
more than doubles.

### Phase 1 — provider seam ⏸ gated on phase 0

Only if phase 0 passes. Not yet greenlit — do not start this alongside the spike.

- Add a `Provider` field to `brain.Config` (`llm.go:81`) — a closed enum, `anthropic` |
  `deepseek`, defaulting to `anthropic`.
- Extend `newBrainLLM` (`wiring.go:389`) to pick base URL + key by provider.
- If phase 0 shows thinking blocks must be replayed: add a `Thinking []ThinkingBlock` (or an
  opaque `Raw` passthrough) to `LLMMessage`, populated in `fromSDKMessage` and re-emitted in
  `toSDKMessages`. Provider-neutral — Anthropic tolerates it too.
- Give the DeepSeek path its own usage logging (§4.4).
- `DEEPSEEK_API_KEY` as a deployment-global env, mirroring `ANTHROPIC_API_KEY`
  (`main.go:185`). A per-user key can come later via the existing dormant
  `rc.AnthropicAPIKey` pattern (`identity/service.go:618`) and the `Integrations` card.

### Phase 2 — the selector ⏸ gated on phase 0

**Shape is decided (§6.1): per-project, on the existing Settings project card, gated behind
an env flag so it is not a visible end-user dropdown.**

- Re-add `brain_model` to `MeProject` + `ProjectUpdateRequest` in `schema/openapi.yaml`, as a
  **closed enum** (`""` | `claude-sonnet-5` | `deepseek-v4-flash` | `deepseek-v4-pro`), `""`
  meaning "deployment default". Regenerate both sides with `make schema` — never hand-edit
  the generated types.
- Restore the identity column + domain field + service validation, modeled exactly on
  `merge_gate_mode` (`identity/entities.go:63-91`, `service.go:431-438`) — same
  normalize-and-validate shape, same "empty means default" semantics.
- `buildTenantProviders` (`wiring.go:341-357`) reads `rc.Project.BrainModel` in place of the
  hardcoded `cfg.BrainModel` fallback, deriving the provider from the model. The tenant
  registry already invalidates on project-config write (`tenant/registry.go:205`), so a model
  change takes effect on the next event with no restart.
- One `<select>` in `ConfigFields.tsx`, next to the merge-gate control, **rendered only when
  the gate flag is on** (see §6.1). The field stays in the wire contract unconditionally —
  gating the control, not the schema, keeps the generated types stable and means flipping the
  flag needs no regen.

### Phase 3 — evaluate ⏸ gated on phase 0

Run the live eval set (`06` §9) on both providers over the same fixtures. Compare tool-call
error rate against the 7.7% baseline and pass latency against p50 13.6 s / p90 31.5 s.

## 6. Reconciling with #111 (`ae9e26a`, "make brain model backend-only")

`ae9e26a` removed `brain_model` from the wire contract, the identity domain, the Postgres
column, the bootstrap seed, and the dashboard form — and `997e22c` had hidden the selector
before that. This proposal re-adds all of it. That reversal should be a conscious decision,
not a footnote, so here is the case.

**What #111 was actually about.** Reading the commit, the objection was to a *free-text model
id as an end-user product concept*: every project ran the deployment default anyway, the
field was already hidden, and carrying it through five layers bought nothing. That reasoning
is sound and this proposal does not dispute it.

**What is different now.** #111 was written when there was exactly one provider. The premise
"every project's brain runs the deployment default model" is only obviously correct while
there is nothing to choose *between*. A second provider with a 15–40× cost difference and a
materially different reliability profile makes per-project selection a real question rather
than a vestigial form field.

**How this differs mechanically, so it does not re-acquire #111's problems:**

| #111's objection | This proposal |
| --- | --- |
| Free-text model id | Closed enum, validated server-side; unknown values rejected |
| Presented as an end-user setting | An explicitly-labelled testing knob; ships hidden or behind a flag (see below) |
| Backend default became advisory | Backend default stays authoritative — `""` means "deployment default", and that is the default value |
| Threaded through for no benefit | Exists to A/B two providers on live traffic, which is the only honest way to evaluate §4.2 and §4.3 |

**On "per-user".** The ticket asked for a per-*user* setting in Settings. Kiln's brain is
constructed per-*project* (`buildTenantProviders`), and Settings is already an account page
holding a card per project. A genuinely per-user setting would need a new user-level config
field that the per-project brain build then reads — more plumbing for the same effect.
**Decided: per-project.** It is per-user in the sense that a user's projects are their own.

### 6.1 Decisions (2026-08-06)

| Question | Decision |
| --- | --- |
| Run the §5 phase-0 spike? | **Yes** — greenlit, ~1 day, kill criteria as written |
| DeepSeek credentials | Being provisioned (`DEEPSEEK_API_KEY`) |
| Selector scope | **Per-project**, on the existing Settings project card — not per-user |
| Selector visibility | **Gated** (env flag / debug-only), not a visible dropdown |
| Phases 1–3 | Gated on phase 0 passing; not greenlit |

The gating decision is the one that keeps this consistent with #92 and #111 rather than
quietly reversing them: the selector exists as a testing affordance, and the end-user-facing
model dropdown that both commits deliberately removed **stays** removed. If DeepSeek later
wins on cost *and* holds up on reliability, the right end state is changing `DefaultModel`
and deleting the selector again — not shipping a permanent model picker.

## 7. Still open — deferred until phase 0 reports

Neither of these blocks the spike; both are decided better with its data in hand.

1. If DeepSeek passes, is the target **replacing** `DefaultModel` or **offering both**? That
   answer decides whether phase 2's selector is permanent scaffolding or a stepping stone to
   deleting itself (§6.1).
2. Does the pricing-increase warning (§1) change the calculus? At current rates the margin is
   large enough that even a 5× increase keeps DeepSeek cheaper — worth re-checking the
   published rates when phase 0 lands rather than trusting this document's snapshot.

## 8. Unverified claims — flagged deliberately

These are from DeepSeek's docs or third-party issue reports and are **not** confirmed against
Kiln's actual payloads. Phase 0 exists to confirm them:

- Whether `thinking: {type: disabled}` is honored on the Anthropic-compat endpoint.
- Whether the Anthropic-compat endpoint has the OpenAI-compat path's thinking-replay
  requirement (§4.1) — the issue I found[^1] is against the OpenAI-compat path.
- The real-world rate of §4.2 in Kiln's specific prompt/tool shape.
- What `stop_reason` values are returned; the compat docs do not list them.

### Status: phase 0 deferred (2026-08-06)

**The spike has not been run.** Two blockers, both environmental:

1. **Credentials.** `DEEPSEEK_API_KEY` is not provisioned. `ANTHROPIC_API_KEY` is wanted
   alongside it — not as a fallback, but because the same chained-tool-call harness run
   against Anthropic is the known-good baseline that makes the DeepSeek result legible.
   Anthropic's extended thinking *does* require thinking blocks replayed across chained tool
   calls, so the Anthropic run is what tells us whether a DeepSeek failure is a DeepSeek
   quirk or our harness reproducing a constraint both providers share.
2. **No Go toolchain in the sandbox.** `go` is not on `PATH` (a `~/go` directory exists but
   no binary), so the harness cannot be built or run here even once keys land. Docker *is*
   available, so the practical route is a `golang:1.26` container mounting `backend/` — the
   SDK (`anthropic-sdk-go@v1.56.0`) is already a dependency, so the harness needs no new
   module. Alternatively run it anywhere with a normal Go install; nothing about it is
   sandbox-specific.

Deferred until both are resolved; **not blocking** any other work in this proposal.

Everything above stands as written — this is a gap in *confirmation*, not a change of
position. The consequence is only that §5's kill criteria remain untested, so phases 1–3 stay
gated (§6.1) rather than becoming eligible by default through the passage of time.

The specific thing to run when the keys arrive: a **≥5-call chained tool-use sequence**,
replaying assistant turns exactly as `toSDKMessages` (`brain/llm.go:351`) does today — that
is, with thinking blocks dropped — and then a second pass with them replayed. The delta
between those two runs is the answer to §4.1, which is the criterion the other five spike
questions are subordinate to.

[^1]: `openclaw/openclaw` issue #72044 — DeepSeek-v4-pro thinking mode breaks on multi-turn
    tool-call flows. Closed 2026-04-26; the reported v4.24 fix "only addressed short chains".
    <https://github.com/openclaw/openclaw/issues/72044>

[^2]: `deepseek-ai/DeepSeek-V3` issue #1244 — V4-Pro intermittently emits tool calls as plain
    text in `content`. **Open**, no assignee, no official response.
    <https://github.com/deepseek-ai/DeepSeek-V3/issues/1244>

[^3]: `deepseek-ai/DeepSeek-V3` issue #1376 — V4 rejects `tool_choice="required"` and named
    function `tool_choice`. <https://github.com/deepseek-ai/DeepSeek-V3/issues/1376>

Primary docs: <https://api-docs.deepseek.com/guides/anthropic_api/>,
<https://api-docs.deepseek.com/quick_start/pricing>,
<https://api-docs.deepseek.com/quick_start/rate_limit>,
<https://api-docs.deepseek.com/guides/kv_cache>
