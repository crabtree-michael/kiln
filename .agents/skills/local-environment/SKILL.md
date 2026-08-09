---
name: local-environment
description: Use when bringing the system up locally or figuring out where a service, its state, or its credentials live — the Docker Compose stack, the Amika dev sandbox you work in, and the gate's test database. Spec 02 §1, §3, §4; production topology is spec 10.
---

# Working in the local environment (doc 02 §1, §3, §4)

## What runs where

Development is **entirely local via Docker Compose**. Three services
(`docker-compose.yml`): `db` (Postgres — board state **and** the durable event queue, one
engine), `backend` (the Go monolith: every module under `backend/internal/`), and `frontend`
(the TS/React client behind a vite dev server).

**Production exists** — spec `10-infrastructure.md`, declared in `render.yaml`: one Render web
service (a single Go binary with the frontend embedded) plus managed Postgres 16, deploy-on-merge
to `main`, published at one canonical origin. Spec 10 explicitly lifted 02 §1's "local only"
non-goal, so anything that reads as "v1 never deploys" is stale. Deployment shapes real backend
behaviour — see `runtime-and-api` for the leader lock (Render runs two instances side by side
during a deploy) and the `KILN_PUBLIC_URL` canonical-host rule.

- **Where state lives.** All authoritative state is in Postgres. The backend holds no
  authoritative state between events; a restart/deploy recovers by re-reading Postgres and
  draining the queue table.
- **Trust boundary.** `/backend` is the only trust boundary: it owns Postgres and every
  provider credential (LLM, STT, push, agent providers, GitHub) and is the only writer of
  board state.
- **Migrations ship embedded.** Each `internal/*/postgres` package `go:embed`s its
  `migrations/*.sql` and the composition root applies them at startup from the embedded FS, so
  the single static binary carries them (the distroless image has no source tree). Add a `.sql`
  file and it is picked up automatically — there is no migrations directory to configure.
  Application is tracked per file in a `schema_migrations` ledger keyed by each module's
  `MigrationsKey`.

## How to work here

- **First-time setup:** `cp .env.example .env` at the repo root (compose reads it
  automatically; never commit the real `.env`, and never print its contents). Keys may stay
  blank until a surface area needs them.
- **Bring it up:** `make up` (= `docker compose up --build`), or `docker compose up -d db
  backend` for just the backend stack. `make down` tears down **and deletes volumes** (`-v`) —
  it wipes Postgres.
- **Keyless stack:** `make up-keyless` overlays `docker-compose.keyless.yml`, flipping every
  paid boundary to an in-process mock and seeding the `e2e-user` project. No keys, no bill, no
  onboarding step. This is the right stack for most local work and for e2e (see
  `end-to-end-development`).
- **Ports:** Postgres `5432` (user/pass/db all `kiln`), backend `8080`, frontend dev server
  `5173`. Backend reaches the db at `postgres://kiln:kiln@db:5432/kiln?sslmode=disable`.
- **Reset the database:** `docker compose down -v && docker compose up -d db`.
- **Check health:** `docker compose ps` (db has a `pg_isready` healthcheck; backend waits on
  it) and `docker compose logs backend` (JSON logs; expect `"kiln starting"` then
  `"kiln serving" addr=":8080"`). **`GET /healthz`** is the liveness+DB probe — 200
  `{status:ok, version}` when the DB ping succeeds, 503 `{status:degraded}` otherwise. It is
  mounted outside `/api`, so it needs no session and no project; `GET /api/board` is
  project-scoped and 404s for a signed-in user with no project, which makes it a poor probe.

## Provisioning the sandbox (the dev box you are working in)

An Amika sandbox is **not** a ready dev box out of the base image: it ships Node 22 + pnpm and
little else. The toolchain, the integration-test database, and the docker daemon are provisioned
by script, and the whole thing is meant to be baked into a snapshot so agents land ready.

- **`make sandbox`** (`scripts/amika/provision.sh`) — the once-per-box provision: Go (version
  tracked from `backend/go.mod`), golangci-lint (pinned to the version in
  `.github/workflows/check.yml`), oapi-codegen, `jq`/`ripgrep`/`build-essential`, a local
  PostgreSQL 16 with the `kiln` role and the `kiln`/`kiln_test` databases, docker + compose v2,
  the git hooks, `.env`, and the Playwright chromium browser **plus its system libraries**.
  Idempotent — re-running it is a fast no-op, so it is safe from any lifecycle hook.
- **`make services`** (`scripts/amika/start-services.sh`) — the once-per-**boot** half. Run it
  after a resume if `pg_isready` or `docker info` fails.
- **`scripts/amika/setup.sh`** — the per-boot lifecycle script Amika itself runs (both
  `setup_script` and `start_script` in `.amika/config.toml`): dependency sync + `make services`.
- **Capturing a snapshot:** run `make sandbox`, confirm `make check` is green, then capture. The
  provisioned state (packages, `/etc/environment`, `/etc/profile.d/kiln-toolchain.sh`) persists;
  the running daemons do not, which is what `make services` is for on the next boot.

## Common footguns

- **`BASH_ENV=/etc/environment` silently un-installs the toolchain for every bash script.** The
  sandbox sets `BASH_ENV`, so bash sources that file for **every non-interactive shell** — and
  its `PATH=` line *overwrites* the PATH the shell inherited. Anything with a
  `#!/usr/bin/env bash` shebang — the git hooks, `scripts/amika/setup.sh`, any helper you write
  — sees a stock PATH with no `/usr/local/go/bin` and no `~/go/bin`, and fails as though nothing
  were installed. **Exporting from `~/.zshrc` or `~/.profile` does not fix this**: bash scripts
  never read them. The system PATH in `/etc/environment` is the only place that reaches every
  shell, which is why `provision.sh` edits it. Note also that a bare `FOO=bar` in that file is
  **not exported** to grandchildren (`go test` under `make`) when the var wasn't already in the
  environment — hence the matching `export` in `/etc/profile.d/kiln-toolchain.sh`.
- **A shell opened *before* provisioning keeps the old PATH.** Agent tool shells often replay a
  PATH captured when the session started, which overrides `/etc/environment` — so right after
  running `make sandbox` you can still get `gofmt: not found` from `make check` in the same
  session while a fresh shell is fine. Use `bash -lc 'make check'` (or start a new shell) to
  confirm; the next boot picks it up normally.
- **No systemd, so nothing starts itself.** `apt-get install postgresql docker.io` succeeds but
  `invoke-rc.d` is denied ("policy-rc.d denied execution of start"), so neither daemon is
  running — after an install *or* a resume from snapshot. Start them with `make services`
  (`pg_ctlcluster 16 main start`, and `dockerd` under `nohup` since there is no unit to start).
  Docker group membership only applies to a **new login**, so `start-services.sh` also grants the
  current user the socket directly (`setfacl`) or every `docker` call needs sudo for that boot.
- **Integration tests skip themselves when `TEST_DATABASE_URL` is unset — the gate goes quiet,
  not red.** `go test -tags=integration ./...` exits 0 with everything skipped, so `make check`
  looks green while testing nothing. The local cluster + the `/etc/environment` entry exist to
  make the sandbox's gate as real as CI's; same credentials as CI.
- **The sandbox's test cluster is on 5433, not 5432.** Compose's `db` publishes 5432 on the
  host, so a native cluster on the default port makes the whole stack fail to start with
  `failed to bind host port 0.0.0.0:5432/tcp: address already in use`. The provisioned cluster
  therefore listens on **5433** (`TEST_DATABASE_URL=postgres://kiln:kiln@localhost:5433/kiln_test?sslmode=disable`)
  so the gate's database and `make up` can run at the same time — the normal case for an agent.
  `sudo` drops `PGPORT`, so pass `-p 5433` to `psql`/`createdb` by hand.
- **A long-lived `kiln_test` used to freeze at the schema it was created with.** Each
  integration suite bootstrapped the shared database with "does my first table exist? then assume
  the schema is current", so every migration added *after* the box was provisioned was skipped
  forever, and the gate failed with `column "…" does not exist` against a column that plainly
  exists in the repo — a stale box reading as broken tests. The suites now apply migrations
  through the **same per-file `schema_migrations` ledger the app uses**
  (`internal/testutil/ApplyMigrations`), so a new migration reaches an existing database on the
  next `make check`. The migrations are *not* written idempotently (bare `CREATE TABLE` / `ADD
  COLUMN`), which is why only a ledger can decide what still needs applying. If you meet a
  database older than the ledger itself, `make test-db-reset` recreates `kiln_test` empty — it
  holds only test scratch, every suite truncates on setup, and `make sandbox` performs that
  repair automatically. (`internal/agent` and `internal/steward` deliberately opt out and drop
  and rebuild their single table per test, so "tables exist but the ledger is empty" is a normal
  mid-run state, not drift.)
- **A blank `.env` breaks the e2e suite in a way that looks like a stack problem.** The
  documented first-time step (`cp .env.example .env`) leaves `KILN_BOOTSTRAP_GITHUB_USER`
  **defined but empty**, `tests/playwright.config.ts` loads that file, and an empty string is not
  nullish — so a `?? 'e2e-user'` default does not fire and the specs mint sessions with an empty
  login, failing with `dev session mint failed: ... -> 400 ... is the stack up?`. The stack is
  fine; the login is empty. Read env vars as `process.env.X?.trim() || default` in `/tests`.
- **The Playwright browser and its system libraries are two installs, and the browser cache
  proves nothing about the libraries.** With `~/.cache/ms-playwright` populated but the shared
  libraries missing, every browser-driven spec dies at launch with
  `error while loading shared libraries: libglib-2.0.so.0` — which reads like a broken browser
  install, not a missing apt package. Fix with `playwright install-deps chromium`, run **from
  `tests/`**: playwright is a dependency of that package, and `pnpm exec` at the repo root (no
  package, no workspace) exits `ERR_PNPM_RECURSIVE_EXEC_NO_PACKAGE`. `provision.sh` probes the
  two independently so a box with one and not the other repairs itself on re-run.
- **`golangci-lint@latest` on the unversioned module path installs v1, which cannot lint this
  repo.** `.golangci.yml` is `version: "2"` format, but
  `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest` resolves to v1.64.x
  (the v2 releases live under the `/v2` module path) — it installs cleanly and then fails at
  lint time. Install via the upstream `install.sh` pinned to the CI version instead.
- **A fresh non-keyless stack has no project, so `/api/*` and the board are empty until you
  onboard one** (spec 11). Every route is project-scoped; a signed-in user with no project gets
  a 404 from `GET /api/board` and the client shows the guided setup flow instead of the board.
  `make up-keyless` seeds a project for you; on a real-service stack, seed one once per fresh DB
  (recipe in `/tests/README.md`). `make down` deletes the DB volume, so re-seed after.
- **The backend Dockerfile's `golang:X-alpine` build image must satisfy the `go` directive in
  `backend/go.mod`** — bumping the toolchain in go.mod without bumping the Dockerfile breaks
  `docker compose build` with "go.mod requires go >= X".
- **Frontend proxy target is container-relative.** The client talks same-origin (`/api/...`,
  `transport.ts`) and the vite dev server proxies `/api` (and `/auth`) to the backend. In compose
  the backend is the `backend` **service**, not `localhost` — so the frontend service sets
  `KILN_PROXY_TARGET=http://backend:8080` (`vite.config.ts` reads it, default `localhost:8080`
  for a bare `pnpm dev`). Point it at `localhost` inside the container and every `/api` hop —
  board fetch *and* the SSE stream — 500s with ECONNREFUSED, so the board stays `reconnecting`.
- Storing authoritative state anywhere but Postgres.
