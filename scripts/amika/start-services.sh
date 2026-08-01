#!/usr/bin/env bash
# Start the local services a Kiln dev box needs, on a box with no systemd.
#
# Amika sandboxes run without a service manager: apt installs postgres and docker
# but `invoke-rc.d` is denied, so nothing is running after a package install or a
# resume-from-snapshot. Both daemons therefore have to be started by hand, once
# per boot — that is what this does.
#
#   postgres -> `go test -tags=integration` (skips itself with no database)
#   dockerd  -> `make up` / `make up-keyless` / `make e2e`
#
# Idempotent and best-effort: already-running is success, and a daemon that will
# not start is a warning, never a hard failure — a box without docker can still
# run the commit gate. Needs passwordless sudo; without it, it does nothing.
set -uo pipefail

log() { printf 'services: %s\n' "$*"; }
warn() { printf 'services: WARNING %s\n' "$*" >&2; }

if ! sudo -n true 2>/dev/null; then
  warn "no passwordless sudo — cannot start services"
  exit 0
fi

# --- PostgreSQL ---------------------------------------------------------------
# The test cluster lives on 5433, not 5432: compose's `db` publishes 5432 on the
# host, and a native cluster squatting there makes `make up` fail to bind. Read
# the port off the cluster itself so this keeps working if provision.sh changes
# it, falling back to 5433.
if command -v pg_ctlcluster >/dev/null 2>&1; then
  pg_port="$(pg_lsclusters -h 2>/dev/null | awk 'NR==1 {print $3}')"
  [ -n "$pg_port" ] || pg_port=5433
  if pg_isready -q -p "$pg_port" 2>/dev/null; then
    log "postgres already accepting connections on $pg_port"
  else
    version="$(pg_lsclusters -h 2>/dev/null | awk 'NR==1 {print $1}')"
    cluster="$(pg_lsclusters -h 2>/dev/null | awk 'NR==1 {print $2}')"
    if [ -n "$version" ] && [ -n "$cluster" ]; then
      log "starting postgres $version/$cluster on $pg_port"
      sudo -n pg_ctlcluster "$version" "$cluster" start 2>/dev/null
      for _ in $(seq 1 20); do pg_isready -q -p "$pg_port" 2>/dev/null && break; sleep 1; done
    fi
    pg_isready -q -p "$pg_port" 2>/dev/null \
      || warn "postgres did not come up (integration tests will skip)"
  fi
fi

# --- Docker -------------------------------------------------------------------
if command -v dockerd >/dev/null 2>&1; then
  if docker info >/dev/null 2>&1; then
    log "docker already running"
  else
    log "starting dockerd"
    sudo -n mkdir -p /var/log/docker
    sudo -n sh -c 'nohup dockerd >>/var/log/docker/dockerd.log 2>&1 &'
    for _ in $(seq 1 30); do sudo -n docker info >/dev/null 2>&1 && break; sleep 1; done
    if sudo -n docker info >/dev/null 2>&1; then
      # Group membership only takes effect on a new login, and agent shells are
      # spawned from the existing session — so grant this user the socket directly
      # or every `docker`/`make up` in this boot needs sudo.
      if ! docker info >/dev/null 2>&1; then
        sudo -n setfacl -m "u:$USER:rw" /var/run/docker.sock 2>/dev/null \
          || sudo -n chmod 666 /var/run/docker.sock 2>/dev/null
      fi
      docker info >/dev/null 2>&1 && log "docker ready" || warn "docker socket not reachable as $USER"
    else
      warn "dockerd did not come up (make up / make e2e unavailable; see /var/log/docker/dockerd.log)"
    fi
  fi
fi

exit 0
