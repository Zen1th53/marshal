#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "$ROOT_DIR"

echo "========================================================="
echo "       MARSHAL Community Production Release Gate        "
echo "========================================================="

FAILURES=0

run_check() {
  local name="$1"
  shift
  printf "%-24s ... " "$name"
  if "$@" > /tmp/marshal-gate-check.log 2>&1; then
    printf "\033[32mPASS\033[0m\n"
    return 0
  else
    printf "\033[31mFAIL\033[0m\n"
    cat /tmp/marshal-gate-check.log | sed 's/^/  [LOG] /'
    FAILURES=$((FAILURES + 1))
    return 1
  fi
}

report_status() {
  local name="$1"
  local status="$2"
  if [[ "$status" == "PASS" ]]; then
    printf "%-24s \033[32mPASS\033[0m\n" "$name"
  elif [[ "$status" == "NOT_RUN" ]]; then
    printf "%-24s \033[33mNOT_RUN\033[0m\n" "$name"
  else
    printf "%-24s \033[31mFAIL\033[0m\n" "$name"
    FAILURES=$((FAILURES + 1))
  fi
}

echo "Running mandatory automated verification..."
echo "---------------------------------------------------------"

# 1. Format, Vet, and Build
run_check "BUILD" bash -c 'test -z "$(gofmt -l .)" && go vet ./... && go build ./...'

# 2. Go Unit Tests
run_check "GO TEST" go test -count=1 ./...

# 3. Race Detector
run_check "RACE" go test -race -count=1 ./internal/app/... ./internal/store/... ./internal/memory/... ./internal/netpolicy/... ./internal/webcontrol/...

# 4. Vulnerability Scan
run_check "VULNERABILITY" bash -c 'if which govulncheck >/dev/null 2>&1; then govulncheck ./...; elif [[ -x "$(go env GOPATH)/bin/govulncheck" ]]; then "$(go env GOPATH)/bin/govulncheck" ./...; else echo "govulncheck not found"; exit 1; fi'

# 5. Sandbox Security
run_check "SANDBOX" go test -count=1 ./internal/sandbox/... ./internal/cell/...

# 6. Network Policy
run_check "NETWORK POLICY" go test -count=1 ./internal/netpolicy/... -run "TestAuthorizeNetworkEgress|TestRunWithRestrictedEgress" ./internal/app

# 7. Authz & Capabilities
run_check "AUTHZ" go test -count=1 ./internal/auth/... ./internal/authz/... ./internal/capability/...

# 8. Memory Subsystem
run_check "MEMORY" go test -count=1 ./internal/memory/... ./conformance/memory/...

# 9. Backup & Restore
run_check "BACKUP/RESTORE" go test -count=1 ./internal/store/... -run "Backup|Restore"

# 10. Web Control Plane
run_check "WEB" bash -c 'cd web && npm run typecheck && npm run lint && npm run test:run && npm run build'

# 11. Packaging & SBOM
run_check "PACKAGING" python3 -m unittest tools/tests/test_build_release.py tools/tests/test_generate_sbom.py

# 12. Clean Install Smoke
run_check "CLEAN INSTALL" python3 -m unittest tools/tests/test_clean_install.py

# 13. Documentation & Manifest
run_check "DOCS & PACK MANIFEST" bash -c 'python3 tools/check_markdown_links.py . && python3 tools/release_verify.py . distribution/PACK-MANIFEST.json'

echo "---------------------------------------------------------"
echo "Evaluating provider qualification matrix..."
echo "---------------------------------------------------------"

# Codex E2E
if [[ "${MARSHAL_TEST_REAL_CODEX:-0}" == "1" ]]; then
  if go test -count=1 -v ./internal/integration -run "TestRealCodex" > /tmp/marshal-codex-e2e.log 2>&1; then
    report_status "CODEX E2E" "PASS"
  else
    report_status "CODEX E2E" "FAIL"
  fi
else
  report_status "CODEX E2E" "NOT_RUN"
fi

# OpenCode + Ollama E2E
if [[ "${MARSHAL_TEST_REAL_OPENCODE:-0}" == "1" ]]; then
  if go test -count=1 -v ./internal/integration -run "TestRealOpenCode" > /tmp/marshal-opencode-e2e.log 2>&1; then
    report_status "OPENCODE+OLLAMA E2E" "PASS"
  else
    report_status "OPENCODE+OLLAMA E2E" "FAIL"
  fi
else
  report_status "OPENCODE+OLLAMA E2E" "NOT_RUN"
fi

# Gemini E2E
if [[ "${MARSHAL_TEST_REAL_GEMINI:-0}" == "1" ]]; then
  if go test -count=1 -v ./internal/integration -run "TestRealGemini" > /tmp/marshal-gemini-e2e.log 2>&1; then
    report_status "GEMINI E2E" "PASS"
  else
    report_status "GEMINI E2E" "FAIL"
  fi
else
  report_status "GEMINI E2E" "NOT_RUN"
fi

# Claude E2E
if [[ "${MARSHAL_TEST_REAL_CLAUDE:-0}" == "1" ]]; then
  if go test -count=1 -v ./internal/integration -run "TestRealClaude" > /tmp/marshal-claude-e2e.log 2>&1; then
    report_status "CLAUDE E2E" "PASS"
  else
    report_status "CLAUDE E2E" "FAIL"
  fi
else
  report_status "CLAUDE E2E" "NOT_RUN"
fi

echo "========================================================="
if [[ $FAILURES -eq 0 ]]; then
  echo -e "\033[32m>> MARSHAL Community Release Gate: PASSED (Ready for Release)\033[0m"
  exit 0
else
  echo -e "\033[31m>> MARSHAL Community Release Gate: FAILED ($FAILURES failures)\033[0m"
  exit 1
fi
