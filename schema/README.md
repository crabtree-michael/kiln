# /schema — the wire contract

`openapi.yaml` is the **single source of truth** for the client↔server boundary
(docs/specs/02 §3). Both sides' types are **generated** from it; the two parallel
agents (backend, frontend) agree on this artifact before writing code.

## Generate both sides

From the repo root:

```bash
make schema
```

That runs two generators:

| Target | Tool | Output | Consumed by |
| ------ | ---- | ------ | ----------- |
| TS types | `openapi-typescript` (frontend devDep) | `frontend/src/schema/generated.ts` | `/frontend` |
| Go types + server | `oapi-codegen` (via `schema/oapi-codegen.yaml`) | `backend/internal/wire/generated.go` | `/backend` |

Both generators are **version-pinned** so everyone's regen is byte-identical —
otherwise `make schema-verify` fails on whoever last regenerated with a
different tool build, which is worse than no check at all. `openapi-typescript`
is an exact version (no caret) in `frontend/package.json`; `oapi-codegen` is
pinned by `OAPI_CODEGEN_VERSION` in the `Makefile` and run via `go run`, so
there is no separately-installed binary to drift. Bumping either means changing
the pin, running `make schema`, and committing the regenerated output together.

## Rules (AGENTS.md)

- **Never hand-edit generated files.** Change `openapi.yaml` and regenerate.
- Schema, generated Go, and generated TS **version together atomically** in one
  commit — `make schema-verify` runs inside `make check`, so the gate regenerates
  and fails if the checked-in output is stale.
- `openapi.yaml` must stay a valid OpenAPI **3.0.x** document (pinned because
  oapi-codegen does not yet fully support 3.1); `make schema` fails loud otherwise.
