---
name: wire-contract
description: How to change the Kiln client<->server boundary — edit /schema/openapi.yaml and regenerate Go + TS types; never hand-edit generated files. Use whenever a change crosses the client<->server boundary (new endpoint, request/response field, live-connection message).
---

# The wire contract (/schema)

**Spec:** `docs/specs/02-initial-technical-architecture.md` §3, §4 + `AGENTS.md`.

## The rule

`schema/openapi.yaml` is the **single source of truth** for the client↔server
boundary. Go and TS types are **generated** from it. **Never hand-edit generated
files.** Change the schema, regenerate, commit all of it together.

## Change flow

```bash
# 1. Edit the contract
$EDITOR schema/openapi.yaml

# 2. Regenerate both sides
make schema
#   -> frontend/src/schema/generated.ts   (openapi-typescript)
#   -> backend/internal/wire/generated.go  (oapi-codegen)

# 3. Both sides now compile against the new types; fix call sites.
make check
```

## Why it works this way

A neutral schema forces the boundary to be **written down and reviewed as its own
artifact** — the contract two parallel agents (backend, frontend) agree on before
writing code. It stays machine-enforced across the Go/TS split.

## CI enforcement

`make schema-verify` regenerates and fails if the checked-in output is stale, so a
schema change without regenerated types **cannot merge**. Schema + both generated
sides version together atomically in one commit.

## Gotchas

- Generated files are git-ignored from editing by convention, listed in
  `.prettierignore` and the eslint `ignores` — don't lint/format them, regenerate.
- Keep `openapi.yaml` valid OpenAPI **3.0.x** (pinned: oapi-codegen doesn't fully
  support 3.1 yet); `make schema` fails loud otherwise.

## Keep this skill current

Record schema conventions (naming, error envelope, versioning) as they solidify.
