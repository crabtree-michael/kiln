#!/usr/bin/env bash
# Amika lifecycle script (see .amika/config.toml [lifecycle]).
#
# The repo's runtime secrets live in a .env kept ONE LEVEL UP from the repo, at
# $HOME/workspace/.env — outside the git tree, so a fresh repo clone on sandbox
# create never clobbers it, and it is captured in the sandbox snapshot. This
# script copies it into the repo on every boot (both the initial `setup_script`
# and each `start_script` resume), so docker-compose finds .env at the repo root.
#
# Idempotent: always overwrites the repo copy from the one-level-up source of
# truth. Never prints the file's contents.
set -euo pipefail

src="$HOME/workspace/.env"        # /home/amika/workspace/.env  (snapshot-baked)
dst="$HOME/workspace/kiln/.env"   # /home/amika/workspace/kiln/.env

if [ -f "$src" ]; then
  cp -f "$src" "$dst"
  echo "inject-env: copied $src -> $dst"
else
  echo "inject-env: WARNING $src not found — is it baked into the snapshot?" >&2
fi
