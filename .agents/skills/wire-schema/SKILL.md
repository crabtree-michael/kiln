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

**Open decisions — TBD → §3/§14.**
- [ ] Schema format (OpenAPI vs JSON-Schema).
- [ ] Code-generation tooling for Go and TS, and where generated code lands.
- [ ] How the generation step is wired into the hard gate.

## How to work here

**Never hand-edit generated types.** Change the schema in `/schema` and regenerate both Go
and TS (AGENTS.md rule). A boundary change means one schema edit and a regen — reviewed as
one artifact.
_(Accumulate: the exact regen command and where output lands, once tooling is chosen.)_

## Common footguns

- Hand-editing generated Go or TS types instead of the schema.
- Changing one side's types to match the other by hand, letting the two sides drift.

_(Accumulate more as you work.)_

## Potential gotchas

_(Accumulate: non-obvious traps and edge cases.)_
