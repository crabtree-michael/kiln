#!/usr/bin/env bash
# Provision a bare Amika sandbox into a ready-to-work Kiln dev box.
#
# This is the *heavy, once-per-box* half of sandbox setup: the system packages,
# language toolchains, and local services an agent needs before it can run the
# hard gate (`make check`). Run it on a fresh dev box, then capture a snapshot —
# every sandbox started from that snapshot lands ready, with no setup delay.
#
#   scripts/amika/provision.sh   <- once, before snapshot capture (this file)
#   scripts/amika/setup.sh       <- every boot, via .amika/config.toml lifecycle
#
# What the gate actually needs, and why each piece is here:
#   * Go            — `go build ./...` + `go test ./...`; version tracks backend/go.mod
#   * golangci-lint — `make lint-backend`; .golangci.yml is v2 format, so v2 ONLY
#   * oapi-codegen  — `make schema` regenerates the Go half of the wire contract
#   * Node 22+pnpm  — frontend lint/typecheck/test (usually already in the image)
#   * PostgreSQL    — `go test -tags=integration` self-skips without a database,
#                     which silently weakens the wall. A local cluster makes the
#                     integration half of the gate real inside the sandbox.
#   * Docker        — `make up` / `make e2e` / `make up-keyless` (the live stack)
#
# Idempotent: safe to re-run, skips whatever is already correct. Needs passwordless
# sudo for the apt/service steps. Handles no secrets and prints none.
set -euo pipefail

log() { printf 'provision: %s\n' "$*"; }
warn() { printf 'provision: WARNING %s\n' "$*" >&2; }

repo_root="${AMIKA_AGENT_CWD:-}"
[ -n "$repo_root" ] || repo_root="$(git rev-parse --show-toplevel 2>/dev/null || true)"
[ -n "$repo_root" ] || repo_root="$HOME/workspace/kiln"
[ -d "$repo_root" ] || repo_root="$(pwd)"
cd "$repo_root"
log "provisioning $repo_root"

sudo_q() { sudo -n "$@"; }

if ! sudo -n true 2>/dev/null; then
  warn "no passwordless sudo — system packages and services will be skipped"
  HAVE_SUDO=0
else
  HAVE_SUDO=1
fi

# --- 0. Hostname resolution ---------------------------------------------------
# Amika names the box (e.g. `sandbox`) without an /etc/hosts entry, so every sudo
# call pays a DNS timeout and prints "unable to resolve host".
if [ "$HAVE_SUDO" = 1 ]; then
  host_name="$(hostname)"
  if ! grep -q "[[:space:]]${host_name}\b" /etc/hosts 2>/dev/null; then
    log "adding $host_name to /etc/hosts"
    printf '127.0.1.1 %s\n' "$host_name" | sudo_q tee -a /etc/hosts >/dev/null
  fi
fi

# --- 1. System packages -------------------------------------------------------
# jq: scripting the toolchain installs below + poking the API by hand.
# ripgrep: agents search this tree constantly.
# postgresql*: the integration-test database.
# build-essential: cgo for anything that needs it.
APT_PACKAGES="jq unzip ripgrep build-essential ca-certificates curl git postgresql postgresql-client"
if [ "$HAVE_SUDO" = 1 ]; then
  missing=""
  for pkg in $APT_PACKAGES; do
    dpkg -s "$pkg" >/dev/null 2>&1 || missing="$missing $pkg"
  done
  if [ -n "$missing" ]; then
    log "apt-get install$missing"
    sudo_q env DEBIAN_FRONTEND=noninteractive apt-get update -qq
    # shellcheck disable=SC2086 # word splitting is the point
    sudo_q env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq $missing
  else
    log "system packages already present"
  fi
fi

# --- 2. Go -------------------------------------------------------------------
# Track the minor the module declares: a toolchain older than backend/go.mod's
# `go` directive fails the build outright ("go.mod requires go >= X").
GO_ROOT=/usr/local/go
want_minor="$(awk '/^go [0-9]/ {print $2; exit}' backend/go.mod 2>/dev/null || true)"
[ -n "$want_minor" ] || want_minor="1.26"

go_ok=0
if [ -x "$GO_ROOT/bin/go" ]; then
  have="$("$GO_ROOT/bin/go" env GOVERSION 2>/dev/null | sed 's/^go//')"
  # Compare as versions: current >= wanted means we're done.
  if [ "$(printf '%s\n%s\n' "$want_minor" "$have" | sort -V | head -1)" = "$want_minor" ]; then
    log "Go $have already installed (go.mod wants >= $want_minor)"
    go_ok=1
  fi
fi

if [ "$go_ok" = 0 ]; then
  # Newest stable release on the wanted minor, falling back to the overall latest.
  go_version="$(curl -sS "https://go.dev/dl/?mode=json" 2>/dev/null \
    | jq -r --arg m "go$want_minor" '[.[] | select(.stable) | .version | select(startswith($m + "."))] | first // empty' 2>/dev/null || true)"
  [ -n "$go_version" ] || go_version="$(curl -sS "https://go.dev/VERSION?m=text" 2>/dev/null | head -1)"
  if [ -z "$go_version" ]; then
    warn "could not determine a Go version to install"
  else
    log "installing $go_version to $GO_ROOT"
    tarball="/tmp/${go_version}.linux-amd64.tar.gz"
    curl -sSL -o "$tarball" "https://go.dev/dl/${go_version}.linux-amd64.tar.gz"
    sudo_q rm -rf "$GO_ROOT"
    sudo_q tar -C /usr/local -xzf "$tarball"
    rm -f "$tarball"
  fi
fi
export PATH="$GO_ROOT/bin:$HOME/go/bin:$PATH"

# --- 3. Go dev tools ----------------------------------------------------------
# golangci-lint is PINNED to the version CI installs, read straight out of the
# workflow so the two cannot drift. Note the v2 module path: `@latest` on the
# unversioned path resolves to v1.64.x, which cannot read a `version: "2"`
# .golangci.yml — installing it looks like success and fails at lint time.
GOLANGCI_VERSION="$(grep -oE 'v2\.[0-9]+\.[0-9]+' .github/workflows/check.yml 2>/dev/null | head -1 || true)"
[ -n "$GOLANGCI_VERSION" ] || GOLANGCI_VERSION="v2.12.2"

if command -v go >/dev/null 2>&1; then
  have_lint="$(golangci-lint version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1 || true)"
  if [ "v$have_lint" != "$GOLANGCI_VERSION" ]; then
    log "installing golangci-lint $GOLANGCI_VERSION"
    curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh \
      | sh -s -- -b "$(go env GOPATH)/bin" "$GOLANGCI_VERSION" >/dev/null \
      || warn "golangci-lint install failed (make lint will be unavailable)"
  else
    log "golangci-lint $have_lint already installed"
  fi

  # oapi-codegen is pinned to the version that generated the CHECKED-IN wire
  # package, read out of that file's own header so the two cannot drift. `@latest`
  # is the same trap golangci-lint was: v2.8.0 renames enum constants
  # (`Unknown` -> `SnapshotStateUnknown`) relative to the v2.7.1 that produced
  # backend/internal/wire, so a box provisioned with @latest turns `make schema`
  # into a diff unrelated to whatever the agent actually changed, and leaves
  # `make schema-verify` red on a clean tree. CI has no schema step, so this bites
  # only inside the sandbox — nothing upstream would catch it.
  #
  # Reading the pin from the generated header is deliberately self-maintaining:
  # regenerate with a newer version on purpose and commit, and the next provision
  # follows the file.
  OAPI_VERSION="$(grep -oE 'oapi-codegen/v2 version v[0-9]+\.[0-9]+\.[0-9]+' backend/internal/wire/generated.go 2>/dev/null \
    | grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' | head -1 || true)"
  [ -n "$OAPI_VERSION" ] || OAPI_VERSION="v2.7.1"

  have_oapi="$(oapi-codegen --version 2>/dev/null | grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' | head -1 || true)"
  if [ "$have_oapi" != "$OAPI_VERSION" ]; then
    log "installing oapi-codegen $OAPI_VERSION"
    go install "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$OAPI_VERSION" \
      || warn "oapi-codegen install failed (make schema will be unavailable)"
  else
    log "oapi-codegen $have_oapi already installed"
  fi
fi

# --- 4. Node -----------------------------------------------------------------
if command -v node >/dev/null 2>&1; then
  log "node $(node --version), pnpm $(pnpm --version 2>/dev/null || echo MISSING)"
  command -v pnpm >/dev/null 2>&1 || warn "pnpm missing — frontend gate will not run"
else
  warn "node missing — the frontend half of the gate will not run"
fi

# --- 5. PostgreSQL: the integration-test database ------------------------------
# The `-tags=integration` suites skip themselves when TEST_DATABASE_URL is unset.
# Same credentials as CI (.github/workflows/check.yml), so a test that passes here
# passes there.
#
# On 5433, NOT the default 5432, because compose's `db` service publishes 5432 on
# the host: with the native cluster on the default port, `make up` dies with
# "failed to bind host port 0.0.0.0:5432/tcp: address already in use" and the
# whole stack refuses to start. Moving the test cluster one port over lets the
# gate's database and the live stack run at the same time, which is the normal
# case for an agent (run the stack, keep testing).
TEST_DB_PORT=5433
TEST_DB_URL="postgres://kiln:kiln@localhost:$TEST_DB_PORT/kiln_test?sslmode=disable"
PG_CONF=/etc/postgresql/16/main/postgresql.conf
if [ "$HAVE_SUDO" = 1 ] && [ -f "$PG_CONF" ] && ! grep -qE "^port = $TEST_DB_PORT" "$PG_CONF"; then
  log "moving the test cluster to port $TEST_DB_PORT (compose's db owns 5432)"
  sudo_q sed -i "s/^port = .*/port = $TEST_DB_PORT/" "$PG_CONF"
  pg_isready -q -p "$TEST_DB_PORT" 2>/dev/null || sudo_q pg_ctlcluster 16 main restart 2>/dev/null || true
fi
export PGPORT="$TEST_DB_PORT"
if [ "$HAVE_SUDO" = 1 ] && command -v pg_ctlcluster >/dev/null 2>&1; then
  # The image has no systemd, so apt's invoke-rc.d never starts the cluster.
  if ! pg_isready -q -p "$TEST_DB_PORT" 2>/dev/null; then
    log "starting the postgres cluster"
    sudo_q pg_ctlcluster 16 main start 2>/dev/null || warn "could not start postgres"
    for _ in $(seq 1 20); do pg_isready -q -p "$TEST_DB_PORT" 2>/dev/null && break; sleep 1; done
  fi
  # sudo drops PGPORT, so every client call names the port explicitly.
  if pg_isready -q -p "$TEST_DB_PORT" 2>/dev/null; then
    if ! sudo_q -u postgres psql -p "$TEST_DB_PORT" -tAc "SELECT 1 FROM pg_roles WHERE rolname='kiln'" | grep -q 1; then
      log "creating role kiln"
      sudo_q -u postgres psql -p "$TEST_DB_PORT" -qc "CREATE ROLE kiln LOGIN PASSWORD 'kiln' SUPERUSER;" >/dev/null
    fi
    for db in kiln kiln_test; do
      if ! sudo_q -u postgres psql -p "$TEST_DB_PORT" -tAc "SELECT 1 FROM pg_database WHERE datname='$db'" | grep -q 1; then
        log "creating database $db"
        sudo_q -u postgres createdb -p "$TEST_DB_PORT" -O kiln "$db"
      fi
    done
  else
    warn "postgres not reachable on $TEST_DB_PORT (integration tests will skip)"
  fi
fi

# --- 6. Docker: the live stack (make up / make e2e) ---------------------------
if [ "$HAVE_SUDO" = 1 ]; then
  if ! command -v docker >/dev/null 2>&1; then
    log "installing docker + compose v2"
    sudo_q env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq docker.io docker-compose-v2 \
      || warn "docker install failed (make up / make e2e will be unavailable)"
  fi
  if command -v docker >/dev/null 2>&1; then
    id -nG "$USER" | tr ' ' '\n' | grep -qx docker || sudo_q usermod -aG docker "$USER"
    "$repo_root/scripts/amika/start-services.sh" || warn "could not start the docker daemon"
  fi
fi

# --- 7. Shell environment ------------------------------------------------------
# The box sets BASH_ENV=/etc/environment, so bash sources that file for EVERY
# non-interactive shell — and its PATH= line overwrites the PATH it inherited.
# That silently un-installs the toolchain for every `#!/usr/bin/env bash` script,
# including the git hooks (.githooks/pre-commit runs the lint+typecheck gate) and
# setup.sh itself: they see a stock PATH with no Go and no ~/go/bin, and fail as
# if nothing were installed. Editing rc files does NOT fix this — bash scripts do
# not read them. The system PATH is the only place that reaches every shell.
# /etc/environment is not a script, so the paths must be literal (no $HOME).
if [ "$HAVE_SUDO" = 1 ] && [ -f /etc/environment ]; then
  if ! grep -q "$GO_ROOT/bin" /etc/environment; then
    log "adding the toolchain to the system PATH (/etc/environment)"
    sudo_q sed -i "s|^PATH=\"|PATH=\"$GO_ROOT/bin:$HOME/go/bin:|" /etc/environment
  fi
  # Same reasoning for the test database: a hook-invoked `make check` that cannot
  # see TEST_DATABASE_URL skips every integration test and still exits 0 — the
  # gate goes quiet rather than red. These are the local dev credentials CI and
  # compose already use, not a secret.
  if ! grep -q '^TEST_DATABASE_URL=' /etc/environment; then
    log "adding TEST_DATABASE_URL to the system environment"
    printf 'TEST_DATABASE_URL="%s"\n' "$TEST_DB_URL" | sudo_q tee -a /etc/environment >/dev/null
  fi

  # /etc/environment is a plain KEY=VALUE file (no `export`, no expansion): the
  # session picks it up because the sandbox seeds the agent environment from it,
  # and bash re-sources it per shell. But a bare assignment in a sourced file is
  # NOT exported, so in the one case where the var is missing from the
  # environment entirely, a bash script would set it and `go test` — its
  # grandchild — still would not see it. profile.d closes that: login shells get
  # a real export.
  profile_d=/etc/profile.d/kiln-toolchain.sh
  if [ ! -f "$profile_d" ]; then
    log "writing $profile_d"
    sudo_q tee "$profile_d" >/dev/null <<EOF
# Kiln dev box (scripts/amika/provision.sh) — toolchain + test database.
export PATH="$GO_ROOT/bin:$HOME/go/bin:\$PATH"
export TEST_DATABASE_URL="$TEST_DB_URL"
EOF
  fi
fi

# Interactive shells additionally get TEST_DATABASE_URL from their rc file. (Vars
# already exported in the environment survive the BASH_ENV re-source above — only
# PATH is clobbered, because only PATH is re-assigned there.)
env_block_marker="# --- Kiln sandbox toolchain (scripts/amika/provision.sh) ---"
for rc in "$HOME/.profile" "$HOME/.bashrc" "$HOME/.zshrc"; do
  [ -e "$rc" ] || continue
  if ! grep -qF "$env_block_marker" "$rc"; then
    log "adding the toolchain env block to $(basename "$rc")"
    {
      printf '\n%s\n' "$env_block_marker"
      printf 'export PATH="%s/bin:$HOME/go/bin:$PATH"\n' "$GO_ROOT"
      printf '# Integration tests self-skip when this is unset, which silently weakens the gate.\n'
      printf 'export TEST_DATABASE_URL="%s"\n' "$TEST_DB_URL"
    } >> "$rc"
  fi
done

# --- 8. Repo: hooks, env file, dependencies ------------------------------------
# The hard-gate hooks (pre-commit = lint+typecheck, pre-push = full gate).
make hooks >/dev/null 2>&1 || git config core.hooksPath .githooks

# Compose reads the repo-root .env. Real keys are baked into the base snapshot;
# this only guarantees the file exists so nothing fails on a missing path.
if [ ! -f .env ] && [ -f .env.example ]; then
  log "creating .env from .env.example (keys blank until a surface area needs them)"
  cp .env.example .env
fi

# Agents commit directly to main (AGENTS.md), so the box needs a git identity.
git config user.name >/dev/null 2>&1 || git config user.name "Kiln Agent"
git config user.email >/dev/null 2>&1 || git config user.email "agent@trykiln.dev"

# Project dependencies — the same work the per-boot script does.
"$repo_root/scripts/amika/setup.sh"

# --- 9. Playwright: the browser AND its system libraries (e2e) -----------------
# Two separate installs, and the e2e run needs both: `install-browser` unpacks the
# chromium build under ~/.cache/ms-playwright, `install-deps` apt-installs the
# shared libraries that build links against. A box with the browser but not the
# libraries looks provisioned and then dies at launch with
#   error while loading shared libraries: libglib-2.0.so.0
# failing every browser-driven spec.
#
# They are probed independently on purpose: keying the libraries off the browser
# cache means a box that got one and not the other never repairs itself on re-run.
if [ -d tests ] && command -v pnpm >/dev/null 2>&1; then
  if [ ! -d "$HOME/.cache/ms-playwright" ]; then
    log "installing the playwright chromium browser"
    (cd tests && pnpm install && pnpm run install-browser) >/dev/null 2>&1 \
      || warn "playwright browser install failed (make e2e will be unavailable)"
  else
    log "playwright browser already installed"
  fi

  # install-deps has to run from tests/: playwright is a dependency of THAT
  # package, and `pnpm exec` at the repo root — which is neither a package nor a
  # workspace — exits ERR_PNPM_RECURSIVE_EXEC_NO_PACKAGE. Sending that to
  # /dev/null under `|| true` is exactly what hid it: the libraries were never
  # installed and provisioning still reported success.
  if [ "$HAVE_SUDO" = 1 ]; then
    if ldconfig -p 2>/dev/null | grep -q 'libglib-2.0\.so\.0'; then
      log "playwright system libraries already present"
    else
      log "installing the playwright system libraries"
      (cd tests && sudo_q env "PATH=$PATH" pnpm exec playwright install-deps chromium) >/dev/null 2>&1 \
        || warn "playwright system-library install failed (browser-driven e2e specs will not launch)"
    fi
  fi
fi

log "done — verify with: make check"
