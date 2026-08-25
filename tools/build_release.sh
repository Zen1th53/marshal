#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 4 ]]; then
  echo "usage: $0 VERSION COMMIT BUILD_DATE OUTPUT_DIR" >&2
  exit 2
fi

release_version=$1
release_commit=$2
release_date=$3
release_output=$4

if [[ ! $release_version =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "invalid semantic version: $release_version" >&2
  exit 2
fi
if [[ ! $release_commit =~ ^[0-9a-f]{7,40}$ ]]; then
  echo "invalid commit: $release_commit" >&2
  exit 2
fi
if [[ ! $release_date =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$ ]]; then
  echo "invalid UTC build date: $release_date" >&2
  exit 2
fi
if [[ -z $release_output || $release_output == / ]]; then
  echo "unsafe output directory" >&2
  exit 2
fi

mkdir -p "$release_output"
release_output=$(cd "$release_output" && pwd -P)
if [[ $release_output == / ]]; then
  echo "unsafe output directory" >&2
  exit 2
fi
release_staging=$(mktemp -d "$release_output/.marshal-release.XXXXXX")
trap 'rm -rf "$release_staging"' EXIT

release_plain=${release_version#v}
release_go_version=$(go env GOVERSION)
release_ldflags="-s -w -buildid= -X github.com/Zen1th53/marshal/internal/cli.Version=$release_version -X github.com/Zen1th53/marshal/internal/cli.Commit=$release_commit -X github.com/Zen1th53/marshal/internal/cli.BuildDate=$release_date"
release_files=(
  "marshal_${release_plain}_linux_amd64.tar.gz"
  "marshal_${release_plain}_linux_arm64.tar.gz"
  "marshal_${release_plain}_sbom.spdx.json"
  "build-metadata.json"
  "checksums.txt"
)
for release_file in "${release_files[@]}"; do
  if [[ -e "$release_output/$release_file" ]]; then
    echo "refusing to overwrite release artifact: $release_output/$release_file" >&2
    exit 2
  fi
done

for release_arch in amd64 arm64; do
  release_bundle="$release_staging/bundle-$release_arch"
  mkdir -p "$release_bundle"
  CGO_ENABLED=0 GOOS=linux GOARCH="$release_arch" go build \
    -trimpath -buildvcs=false -ldflags "$release_ldflags" \
    -o "$release_bundle/marshal" ./cmd/marshal
  cp LICENSE LICENSING.md README.md THIRD_PARTY_NOTICES.md release/INSTALL.md "$release_bundle/"
  TZ=UTC tar --sort=name --mtime="$release_date" --owner=0 --group=0 \
    --numeric-owner -C "$release_bundle" -cf - \
    INSTALL.md LICENSE LICENSING.md README.md THIRD_PARTY_NOTICES.md marshal | \
    gzip -n > "$release_output/marshal_${release_plain}_linux_${release_arch}.tar.gz"
done

python3 tools/release_trust.py generate-sbom \
  --repo . \
  --version "$release_version" \
  --created "$release_date" \
  --output "$release_output/marshal_${release_plain}_sbom.spdx.json"

python3 - "$release_version" "$release_commit" "$release_date" "$release_go_version" "$release_output/build-metadata.json" <<'PY'
import json
import pathlib
import sys

version, commit, build_date, go_version, destination = sys.argv[1:]
metadata = {
    "build_date": build_date,
    "commit": commit,
    "go_version": go_version,
    "platforms": ["linux/amd64", "linux/arm64"],
    "version": version,
}
pathlib.Path(destination).write_text(
    json.dumps(metadata, indent=2, sort_keys=True) + "\n", encoding="utf-8"
)
PY

(
  cd "$release_output"
  sha256sum \
    "marshal_${release_plain}_linux_amd64.tar.gz" \
    "marshal_${release_plain}_linux_arm64.tar.gz" \
    "marshal_${release_plain}_sbom.spdx.json" \
    build-metadata.json > checksums.txt
)
