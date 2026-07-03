# Kiln

A cloud orchestrator for autonomous coding agents, driven by voice. You run
several agents in the cloud and manage them by talking to a single orchestrator
through a mobile-first web app.

- **Product design:** [`docs/specs/01-initial.md`](docs/specs/01-initial.md)
- **Technical architecture:** [`docs/specs/02-initial-technical-architecture.md`](docs/specs/02-initial-technical-architecture.md)

## Quickstart

```bash
make setup     # install frontend deps + Go modules
make hooks     # install the pre-commit / pre-push hard-gate hooks
make check     # the hard gate: lint + type-check/build + tests
make up        # run the whole stack locally (Postgres + backend + frontend)
```

Requires Go 1.23+, `golangci-lint` v2, Node 22 + pnpm, and Docker (for `make up`).

## Layout

```
/backend            Go orchestrator: api · runtime · brain · board · amika
/frontend           TS/React mobile-first PWA
/schema             language-neutral wire contract (generates Go + TS types)
.agents/skills      canonical skill library (per surface area + general)
.githooks           the hard-gate git hooks
docker-compose.yml  the whole system on one machine
```

## Working in this repo

Kiln is built largely by coding agents. The development harness — hard gate,
strict linters, and a self-maintaining skill per surface area — is the first
thing built (02 §4). Start with [`AGENTS.md`](AGENTS.md) and the
`end-to-end-development` skill.
