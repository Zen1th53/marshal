#!/bin/sh
# Build the public Marshal engine locally.
set -eu
cd "$(dirname "$0")/.."
version=${1:-v0.2.0}
case "$version" in
  ''|*[!A-Za-z0-9._-]*) echo 'Invalid local version label' >&2; exit 2 ;;
esac
export GOMAXPROCS=${GOMAXPROCS:-2}
export GOPROXY=off GOSUMDB=off
export CCACHE_DISABLE=1
commit=$(git rev-parse HEAD)
build_date=$(date -u +%Y-%m-%dT%H:%M:%SZ)
go build -p 2 -mod=readonly -ldflags "-X github.com/Zen1th53/marshal/internal/cli.Version=${version} -X github.com/Zen1th53/marshal/internal/cli.Commit=${commit} -X github.com/Zen1th53/marshal/internal/cli.BuildDate=${build_date}" -o marshal ./cmd/marshal
