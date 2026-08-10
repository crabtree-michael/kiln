# Investigation — is a project's brain model field actually respected?

**Date:** 2026-08-10 · **Question:** the `projects` row is believed to carry a per-project brain
model; is it read on the brain execution path, or silently overridden by a deployment default? ·
**Code investigated:** `89a1d9e`

Investigation only — no product code changed here. **Answer: there is no per-project brain model
field to respect.** It was removed on purpose in July 2026, so every project's brain runs the
deployment-wide model. One loose end came out of the trace: the removal edited an applied migration
in place, so pre-July databases still physically carry the dead column (§3).

## 1. What actually picks the model

`buildTenantProviders` builds one brain per project and takes the model from deployment config,
never from the project row — `backend/cmd/kiln/wiring.go:360-376`:

```go
model := cfg.BrainModel      // KILN_BRAIN_MODEL
if model == "" { model = brain.DefaultModel }
...
llm := newBrainLLM(cfg, model, effort, scriptedBrain)
brainSvc := brain.NewService(..., brain.Config{Model: model, Effort: effort, GateMode: gateMode})
```

From there the chain is `brain/service.go:142` → `brain/llm.go:351` `Adapter.model()`, whose
precedence is `Config.Model` → `$KILN_BRAIN_MODEL` → `DefaultModel` (`claude-sonnet-5`,
`brain/llm.go:20`). Because wiring always passes a non-empty `Model`, the adapter's own env/default
fallback is unreachable from the composition root — it only serves direct `Adapter` construction.

What makes this deliberate rather than an oversight: `rc.Project` *is* consulted three lines away
for the other per-project knobs — `MergeGateMode` (`wiring.go:375`), `WorkerCount`,
`AgentProvider`. The model is the one knob that isn't, and the comment above it says so.

## 2. Nothing per-project exists to read

`ae9e26a` *"refactor: make brain model backend-only, remove user configuration (#111)"*
(2026-07-12) removed `brain_model` from all five layers at once: the wire contract (`MeProject`,
`ProjectUpdateRequest`), `identity.Project` / `ProjectUpdate`, the Postgres column and the store
projection, the bootstrap env seed, and the dashboard `ProjectFields` form. `997e22c` had already
hidden the selector before that.

The live `projects` columns are `id, owner_user_id, name, repo_url, amika_snapshot, worker_count,
created_at, amika_secrets, merge_gate_mode, agent_provider, deleted_at`. No model column, and the
store's read projection (`identity/postgres/store.go:24`) matches.

So the question "does changing it per project change the model?" has no per-project surface to
change: there is no column, no domain field, no API field, and no control in the dashboard.

## 3. Open: the dead column survives in pre-July databases

`ae9e26a` dropped the column by **editing `0001_identity.sql` in place** — deleting the
`brain_model text NOT NULL DEFAULT ''` line — rather than adding a `DROP COLUMN` migration. The
runner keys its ledger on filename with no checksum (`wiring.go:1101` `applyMigrationFile` skips
anything already recorded), so `0001` never re-runs and no drop is ever issued.

Consequence: a database created before 2026-07-12 — including any production instance — still
carries `brain_model` holding whatever was last written to it, while a fresh database never gets
the column at all. It is inert (every write enumerates columns explicitly, and the leftover has a
default), but it is schema drift between environments, and a column that reads as live config to
anyone inspecting the database.

Not verified against a live database — no local stack was running during this investigation, so
this is read off the ledger semantics plus the absent drop.

**Suggested follow-up ticket:** a `0010_drop_project_brain_model.sql` doing
`ALTER TABLE projects DROP COLUMN IF EXISTS brain_model`. `IF EXISTS` covers both environments in
one file.

## 4. Relation to the DeepSeek proposal

`docs/ticket-draft-deepseek-brain.md` §5 phase 2 proposes re-adding `brain_model` as a per-project
**closed enum** behind an env flag, so a second provider can be chosen per project; its §6 argues
the reversal against #111 explicitly. If that lands, `buildTenantProviders` reads
`rc.Project.BrainModel` in place of `cfg.BrainModel`, and the tenant registry's existing
invalidation on project-config write (`tenant/registry.go:205`) makes a change take effect on the
next event with no restart.

That ticket and the drop in §3 are compatible, and the ordering does not matter: phase 2 would add
a fresh column through a normal migration, so dropping the orphan first simply means both
environments start from the same place.

Until then: **every project's brain runs `KILN_BRAIN_MODEL`**, default `claude-sonnet-5`
(`docker-compose.yml:35` sets it explicitly).
