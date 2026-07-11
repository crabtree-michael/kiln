---
name: wire-schema
description: Use when changing the client↔server contract — the language-neutral wire schema in /schema that generates both Go and TS types. Never hand-edit generated types; change the schema and regenerate both sides. Anchor /schema. Spec 02 §3, §4.
---

# Wire contract / schema (doc 02 §3, §4)

## Functional Requirements

**Responsibility.** The language-neutral wire contract in `/schema` — the client↔server
interface written down and reviewed as its own artifact, generating both Go and TS types
(§3). It is the contract two parallel agents agree on before writing code.

**Interface.** A neutral schema (OpenAPI / JSON-Schema) plus the generation step that emits
Go types (consumed by the API, §7) and TS types (consumed by the web client, §11). Schema,
skills, and both sides of the wire contract **version together atomically** (§4).

**Dependencies.** None upstream. Consumed by the API / runtime (§7) and the web client (§11).

**Decisions (resolved — see `schema/README.md`).**
- [x] Schema format → **OpenAPI 3.0.x** (`schema/openapi.yaml`, the single source of truth).
      Pinned to 3.0.x because oapi-codegen does not yet fully support 3.1; `make schema`
      fails loud if the document isn't valid 3.0.x.
- [x] Code-generation tooling → **`oapi-codegen`** (Go, config in `schema/oapi-codegen.yaml`)
      + **`openapi-typescript`** (TS, a frontend devDep). Output lands in
      `backend/internal/wire/generated.go` (package `wire`, zero external deps) and
      `frontend/src/schema/generated.ts`.
- [x] Wired into the hard gate → `make schema` regenerates both sides; `make schema-verify`
      (CI runs it) regenerates and fails if the checked-in output is stale.

## How to work here

**Never hand-edit generated types.** Change `schema/openapi.yaml` and run **`make schema`**
to regenerate both Go and TS (AGENTS.md rule). A boundary change means one schema edit and a
regen — reviewed as one artifact. Schema, generated Go, and generated TS **version together
atomically** in one commit (§4).

- Regen: `make schema` (root) → `openapi-typescript` writes `frontend/src/schema/generated.ts`,
  `oapi-codegen` writes `backend/internal/wire/generated.go`.
- Verify nothing drifted: `make schema-verify` (fails if regenerating produces a diff).
- Keep `openapi.yaml` a valid **OpenAPI 3.0.x** document (3.1 constructs break oapi-codegen).

## Common footguns

- Hand-editing generated Go or TS types instead of the schema.
- Changing one side's types to match the other by hand, letting the two sides drift.

_(Accumulate more as you work.)_

## Potential gotchas

_(Accumulate: non-obvious traps and edge cases.)_
