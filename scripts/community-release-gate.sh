#!/usr/bin/env bash
set -euo pipefail

gate_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)
gate_logs=$(mktemp -d)
trap 'rm -rf "$gate_logs"' EXIT
cd "$gate_root"

printf '%s\n' \
  "=========================================================" \
  "       MARSHAL Community Production Release Gate        " \
  "========================================================="

gate_failures=0

run_check() {
  local name=$1
  shift
  local log="$gate_logs/${name//[^A-Za-z0-9]/_}.log"
  printf "%-26s ... " "$name"
  if "$@" >"$log" 2>&1; then
    printf "PASS\n"
  else
    printf "FAIL\n"
    sed 's/^/  [LOG] /' "$log"
    gate_failures=$((gate_failures + 1))
  fi
}

run_optional_e2e() {
  local name=$1
  local variable=$2
  local pattern=$3
  local log="$gate_logs/${name//[^A-Za-z0-9]/_}.log"
  if [[ ${!variable:-0} != 1 ]]; then
    printf "%-26s ... NOT_RUN (%s is not 1)\n" "$name" "$variable"
    return
  fi
  printf "%-26s ... " "$name"
  if go test -count=1 -v ./internal/integration -run "$pattern" >"$log" 2>&1; then
    printf "PASS\n"
  else
    printf "FAIL\n"
    sed 's/^/  [LOG] /' "$log"
    gate_failures=$((gate_failures + 1))
  fi
}

printf '%s\n' "Mandatory automated verification" "---------------------------------------------------------"

run_check "BUILD" go build ./...
run_check "GO VET" go vet ./...
run_check "GO TEST" go test -count=1 ./...
run_check "GO TEST RACE" go test -race -count=1 ./...
run_check "VULNERABILITY" bash -c '
  if command -v govulncheck >/dev/null 2>&1; then
    exec govulncheck ./...
  fi
  scanner="$(go env GOPATH)/bin/govulncheck"
  if [[ -x "$scanner" ]]; then
    exec "$scanner" ./...
  fi
  echo "govulncheck not found" >&2
  exit 1
'
run_check "SANDBOX SECURITY" go test -count=1 ./internal/sandbox/... ./internal/cell/...
run_check "POLICY AND AUTHZ" go test -count=1 ./internal/netpolicy/... ./internal/auth/... ./internal/authz/... ./internal/capability/... ./internal/policy/...
run_check "MEMORY AND MIGRATIONS" go test -count=1 ./internal/memory/... ./internal/store/... ./conformance/memory/...
run_check "RESOURCE AWARENESS" go test -count=1 ./internal/resources/... ./internal/doctor/...
run_check "PROVIDER ADAPTERS" go test -count=1 ./internal/adapter/... ./internal/integration/...
run_check "BACKUP AND RECOVERY" go test -count=1 ./internal/store/... -run 'Backup|Restore|Recovery'
run_check "WEB CONTROL PLANE" bash -c '
  cd web
  npm ci
  npm run typecheck
  npm run lint
  npm run test:run
  npm run build
  cd ..
  diff -ru web/dist internal/webcontrol/dist
'
run_check "RELEASE TOOLING" python3 -m unittest \
  tools/tests/test_build_release.py \
  tools/tests/test_release_trust.py
run_check "PACK CONFORMANCE" bash -c '
  python3 conformance/runner.py validate-pack
  python3 -m unittest discover -s conformance/tests -p "test_*.py"
'
run_check "LEGACY TOOLING" python3 -m unittest discover -s tools/tests_v6 -p 'test_*.py'
run_check "CLEAN INSTALL" python3 -m unittest tools/tests/test_clean_install.py
run_check "DOCS AND MANIFEST" bash -c '
  python3 tools/release_verify.py . distribution/PACK-MANIFEST.json
'

printf '%s\n' "---------------------------------------------------------" "Opt-in external provider qualification"
run_optional_e2e "CODEX E2E" MARSHAL_TEST_REAL_CODEX 'TestRealCodex'
run_optional_e2e "OPENCODE+OLLAMA E2E" MARSHAL_TEST_REAL_OPENCODE 'TestRealOpenCode'
run_optional_e2e "GEMINI E2E" MARSHAL_TEST_REAL_GEMINI 'TestRealGemini'
run_optional_e2e "CLAUDE E2E" MARSHAL_TEST_REAL_CLAUDE 'TestRealClaude'
run_optional_e2e "CODEX+CLAUDE PARALLEL" MARSHAL_TEST_REAL_PARALLEL_CODEX_CLAUDE 'TestRealParallelProviderAgentsSharedMemory'
run_optional_e2e "TWO-CODEX PARALLEL" MARSHAL_TEST_REAL_PARALLEL_CODEX_AGENTS 'TestRealParallelProviderAgentsSharedMemory'

printf '%s\n' "========================================================="
if [[ $gate_failures -eq 0 ]]; then
  printf '%s\n' ">> MARSHAL Community Release Gate: PASSED"
  exit 0
fi
printf '%s\n' ">> MARSHAL Community Release Gate: FAILED ($gate_failures failures)"
exit 1
