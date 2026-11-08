#!/usr/bin/env bash
# tools/qualify/qualify.sh
# Local qualification entry point.
# Validates both default and governed builds, the nested code-evidence module,
# conformance suites, tooling tests, documentation, and release verification.
set -euo pipefail

# Qualification must never enroll a test installation with the public service.
export MARSHAL_CLOUD_ENDPOINT=off

gate_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)
gate_logs=$(mktemp -d "${TMPDIR:-/tmp}/marshal-qualify.XXXXXX")
trap 'rm -rf "$gate_logs"' EXIT
cd "$gate_root"

printf '%s\n' \
  "=========================================================" \
  "       Marshal Qualification Gate        " \
  "========================================================="

gate_failures=0

run_check() {
  local name=$1
  shift
  local log="$gate_logs/${name//[^A-Za-z0-9]/_}.log"
  printf "%-32s ... " "$name"
  if "$@" >"$log" 2>&1; then
    printf "PASS\n"
  else
    printf "FAIL\n"
    sed 's/^/  [LOG] /' "$log" | tail -n 25
    gate_failures=$((gate_failures + 1))
  fi
}

printf '%s\n' "1. Marshal Build & Vet" "---------------------------------------------------------"
run_check "COMMUNITY BUILD" go build -buildvcs=false ./...
run_check "COMMUNITY VET" go vet ./...

printf '%s\n' "---------------------------------------------------------" "2. Governed Compatibility Build & Vet"
run_check "GOVERNED BUILD" go build -tags governed -buildvcs=false ./...
run_check "GOVERNED VET" go vet -tags governed ./...

printf '%s\n' "---------------------------------------------------------" "3. Nested Code-Evidence Module"
run_check "CODE-EVIDENCE BUILD" bash -c 'cd third_party/code-evidence && go build ./...'
run_check "CODE-EVIDENCE VET" bash -c 'cd third_party/code-evidence && go vet ./...'
run_check "CODE-EVIDENCE TEST" bash -c 'cd third_party/code-evidence && go test -count=1 ./...'

printf '%s\n' "---------------------------------------------------------" "4. Conformance & Python Suites"
run_check "VALIDATE PACK" python3 conformance/runner.py validate-pack
run_check "CONFORMANCE TESTS" python3 -m unittest discover -s conformance/tests -v
run_check "TOOLS TESTS" python3 -m unittest discover -s tools/tests -v
run_check "TOOLS V6 TESTS" python3 -m unittest discover -s tools/tests_v6 -v

printf '%s\n' "---------------------------------------------------------" "5. Documentation & Manifest"
run_check "PUBLIC DOCS CHECK" bash scripts/docs-check.sh
run_check "SOURCE DOCS CHECK" bash scripts/private-docs-check.sh

printf '%s\n' "---------------------------------------------------------" "6. Edition Verification"
run_check "EDITION INTEGRITY" bash -c '
  com_ver=$(go run -tags governed ./cmd/marshal --json version | python3 -c "import json, sys; d=json.load(sys.stdin); print(d.get(\"edition\", \"community\"))")
  ext_ver=$(go run ./cmd/marshal --json version | python3 -c "import json, sys; d=json.load(sys.stdin); print(d.get(\"edition\", \"\"))")
  if [ "$com_ver" != "community" ] && [ -n "$com_ver" ]; then
    echo "unexpected community edition output: $com_ver" >&2
    exit 1
  fi
  if [ "$ext_ver" != "Marshal" ]; then
    echo "expected Marshal output, got: $ext_ver" >&2
    exit 1
  fi
'

printf '%s\n' "========================================================="
if [[ $gate_failures -eq 0 ]]; then
  printf '%s\n' ">> Marshal Qualification Gate: ALL CHECKS PASSED"
  exit 0
else
  printf '%s\n' ">> Marshal Qualification Gate: FAILED ($gate_failures failure(s))"
  exit 1
fi
