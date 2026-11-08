#!/usr/bin/env bash
# Run one shard of the Go test suite.
#
# Usage: tools/ci/test-shard.sh INDEX COUNT [go test flags...]
#
# Every top-level test is assigned to a shard by a hash of "package.Name", so
# the split is stable, needs no timing data and stays balanced as tests are
# added. All shards together run every test at least once. Selected packages
# run in one `go test` invocation so packages still execute in parallel; a test
# name repeated in two packages may therefore run in a second shard as well,
# which costs time but never skips anything. Set GO_TEST_TAGS to test an
# edition (for example GO_TEST_TAGS=governed).
set -euo pipefail

# Qualification must never enroll a test installation with the public service.
export MARSHAL_CLOUD_ENDPOINT=off

if [ "$#" -lt 2 ]; then
  echo "usage: $0 INDEX COUNT [go test flags...]" >&2
  exit 2
fi
index=$1
count=$2
shift 2
if ! [[ "$index" =~ ^[0-9]+$ && "$count" =~ ^[1-9][0-9]*$ ]] || [ "$index" -ge "$count" ]; then
  echo "invalid shard $index/$count" >&2
  exit 2
fi

tag_args=()
if [ -n "${GO_TEST_TAGS:-}" ]; then
  tag_args=(-tags "$GO_TEST_TAGS")
fi
# Listing compiles the test binaries with the same flags as the run, so the
# build cache is shared between the two.
build_args=()
for arg in "$@"; do
  case "$arg" in
    -race | -cover) build_args+=("$arg") ;;
  esac
done

listing=$(mktemp)
trap 'rm -f "$listing"' EXIT
go test "${tag_args[@]}" "${build_args[@]}" -list '.*' ./... >"$listing"

declare -A selected=()
declare -A pkg_tests=()
total_tests=0
pending=()
while IFS= read -r line; do
  case "$line" in
    ok[[:space:]]*)
      pkg=$(printf '%s\n' "$line" | cut -f2)
      for name in "${pending[@]}"; do
        sum=$(printf '%s.%s' "$pkg" "$name" | cksum | cut -d' ' -f1)
        if [ $((sum % count)) -eq "$index" ]; then
          selected[$pkg]=1
          pkg_tests[$pkg]+="${name} "
          total_tests=$((total_tests + 1))
        fi
      done
      pending=()
      ;;
    Test* | Example* | Fuzz*) pending+=("$line") ;;
  esac
done <"$listing"

if [ "$total_tests" -eq 0 ]; then
  echo "shard $index/$count: no tests"
  exit 0
fi

echo "shard $index/$count: $total_tests tests in ${#selected[@]} packages"

MAX_REGEX_BYTES=16384
batch_pkgs=()
batch_names=()
batch_len=0
status=0

run_batch() {
  if [ "${#batch_pkgs[@]}" -eq 0 ]; then
    return 0
  fi
  local pattern="^($(printf '%s\n' "${batch_names[@]}" | sort -u | paste -sd'|'))\$"
  if ! go test "${tag_args[@]}" "$@" -run "$pattern" "${batch_pkgs[@]}"; then
    status=1
  fi
  batch_pkgs=()
  batch_names=()
  batch_len=0
}

readarray -t sorted_pkgs < <(printf '%s\n' "${!selected[@]}" | sort)

for pkg in "${sorted_pkgs[@]}"; do
  read -ra p_names <<< "${pkg_tests[$pkg]}"
  p_len=0
  for n in "${p_names[@]}"; do
    p_len=$((p_len + ${#n} + 1))
  done

  # If a single package exceeds MAX_REGEX_BYTES on its own, chunk it individually
  if [ "$p_len" -gt "$MAX_REGEX_BYTES" ]; then
    run_batch
    chunk=()
    chunk_len=0
    for n in "${p_names[@]}"; do
      n_len=$(( ${#n} + 1 ))
      if [ "$chunk_len" -gt 0 ] && [ $((chunk_len + n_len)) -gt "$MAX_REGEX_BYTES" ]; then
        pattern="^($(printf '%s\n' "${chunk[@]}" | sort -u | paste -sd'|'))\$"
        if ! go test "${tag_args[@]}" "$@" -run "$pattern" "$pkg"; then
          status=1
        fi
        chunk=()
        chunk_len=0
      fi
      chunk+=("$n")
      chunk_len=$((chunk_len + n_len))
    done
    if [ "${#chunk[@]}" -gt 0 ]; then
      pattern="^($(printf '%s\n' "${chunk[@]}" | sort -u | paste -sd'|'))\$"
      if ! go test "${tag_args[@]}" "$@" -run "$pattern" "$pkg"; then
        status=1
      fi
    fi
    continue
  fi

  if [ "$batch_len" -gt 0 ] && [ $((batch_len + p_len)) -gt "$MAX_REGEX_BYTES" ]; then
    run_batch
  fi
  batch_pkgs+=("$pkg")
  batch_names+=("${p_names[@]}")
  batch_len=$((batch_len + p_len))
done

run_batch
exit "$status"
