#!/bin/sh
# Build a linux/amd64 preview binary of the current checkout, stamped the
# way tools/build_release.sh stamps a release (internal/cli.Version, Commit
# and BuildDate), so `marshal version` names the build, e.g.
# "Marshal v0.3.5-dev+460fcf28 (commit: 460fcf28…)". It is a development
# build: internal/cloud.releaseBuild stays unset, so a preview never reports
# to the production Community Cloud.
#
# usage: tools/build_preview.sh [OUTPUT_DIR]   (default: dist/preview)
# The binary is OUTPUT_DIR/marshal-<version>-linux-amd64; its path is printed.
set -eu
cd "$(dirname "$0")/.."

out=${1:-dist/preview}
case "$out" in
  ''|/) echo 'unsafe output directory' >&2; exit 2 ;;
esac

commit=$(git rev-parse HEAD)
short=$(git rev-parse --short=8 HEAD)
if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
  short="${short}.dirty"
fi
# The next patch after the latest release tag, as a pre-release.
last=$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' 2>/dev/null || echo v0.0.0)
base=${last#v}
base=${base%%-*}
major=${base%%.*}
rest=${base#*.}
minor=${rest%%.*}
patch=${rest#*.}
case "$major$minor$patch" in
  ''|*[!0-9]*) echo "cannot read release tag: $last" >&2; exit 2 ;;
esac
version="v${major}.${minor}.$((patch + 1))-dev+${short}"
build_date=$(date -u +%Y-%m-%dT%H:%M:%SZ)

ldflags="-s -w -X github.com/Zen1th53/marshal/internal/cli.Version=${version} -X github.com/Zen1th53/marshal/internal/cli.Commit=${commit} -X github.com/Zen1th53/marshal/internal/cli.BuildDate=${build_date}"

mkdir -p "$out"
bin="$out/marshal-${version}-linux-amd64"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false \
  -ldflags "$ldflags" -o "$bin" ./cmd/marshal
echo "$bin"
