#!/bin/sh
# Usage: scripts/docker-factory.sh [factory serve args]
# Runs `factory serve` in a Linux container that has bwrap, for hosts that lack it (macOS).
# The token comes from ./claude-token. The UI is at http://localhost:7070.
set -eu
cd "$(dirname "$0")/.."
[ -s claude-token ] || { echo "claude-token is missing or empty; make it with: claude setup-token" >&2; exit 1; }
CLAUDE_CODE_OAUTH_TOKEN=$(tr -d '\n' < claude-token)
export CLAUDE_CODE_OAUTH_TOKEN
# Run commits carry the identity of this repo's git config.
FACTORY_GIT_NAME=$(git config user.name)
FACTORY_GIT_EMAIL=$(git config user.email)
export FACTORY_GIT_NAME FACTORY_GIT_EMAIL
docker build -q -t galvantula-factory docker >/dev/null
# bwrap needs user namespaces (seccomp, apparmor) and a fresh /proc (systempaths); Docker blocks all three by default.
# -e NAME keeps the token off the command line.
# Port 7070 is open to this machine only: the server has no login.
exec docker run --rm --init -i \
  --security-opt seccomp=unconfined --security-opt apparmor=unconfined --security-opt systempaths=unconfined \
  -e CLAUDE_CODE_OAUTH_TOKEN -e FACTORY_GIT_NAME -e FACTORY_GIT_EMAIL -p 127.0.0.1:7070:7070 \
  -v "$PWD":/work -w /work -v galvantula-gocache:/root/.cache/go-build \
  galvantula-factory go run ./cmd/factory serve -addr 0.0.0.0:7070 "$@"
