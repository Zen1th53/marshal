#!/bin/sh
set -eu

root=${1:-$(dirname "$0")/..}
root=$(cd "$root" && pwd -P)
python3 "$root/tools/release_verify.py" "$root" "$root/distribution/PACK-MANIFEST.json" --generate
