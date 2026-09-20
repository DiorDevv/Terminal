#!/usr/bin/env bash
#
# Builds the whole panel into ONE file: bin/squidadmin-backend
#
#   1. builds the frontend (npm ci + vite build)
#   2. copies it into backend/internal/web/dist, which go:embed bakes into the binary
#   3. builds the Go backend (static, no cgo)
#
# Needs Go and Node.js on the machine that builds; the machine that RUNS the
# binary needs neither. Build on a workstation, copy the file to the server and
# install it with:  sudo ./deploy/install.sh --bin bin/squidadmin-backend
#
# Usage:  scripts/build.sh [--skip-frontend]   (reuse an existing frontend/dist)
#
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
SKIP_FRONTEND=0
[[ "${1:-}" == "--skip-frontend" ]] && SKIP_FRONTEND=1

log() { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
die() { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

command -v go >/dev/null || die "Go is not installed"

if [[ $SKIP_FRONTEND -eq 0 ]]; then
  command -v npm >/dev/null || die "Node.js/npm is not installed (or use --skip-frontend with a prebuilt frontend/dist)"
  log "building the frontend"
  cd "$ROOT/frontend"
  npm ci --no-audit --no-fund
  npm run build
fi

[[ -f "$ROOT/frontend/dist/index.html" ]] || die "frontend/dist/index.html is missing: build the frontend first"

log "embedding the frontend"
DIST="$ROOT/backend/internal/web/dist"
find "$DIST" -mindepth 1 ! -name .gitkeep -delete
cp -R "$ROOT/frontend/dist/." "$DIST/"

VERSION=$(git -c safe.directory="*" -C "$ROOT" describe --tags --always --dirty 2>/dev/null || echo dev)
log "building the backend ($VERSION)"
mkdir -p "$ROOT/bin"
(cd "$ROOT/backend" && CGO_ENABLED=0 go build -trimpath -buildvcs=false \
  -ldflags "-s -w -X main.version=$VERSION" \
  -o "$ROOT/bin/squidadmin-backend" ./cmd/server)

log "done: $ROOT/bin/squidadmin-backend ($(du -h "$ROOT/bin/squidadmin-backend" | cut -f1))"
