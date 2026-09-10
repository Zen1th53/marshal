# Process 06 candidate qualification

Candidate base: `a4a4f377630a9c9038dd6ebc3cd2d264773ed5cb`

Process 05 candidate: `e56e7af9ec13906ae9c6a83d288819e9642f37ca`

This record describes candidate qualification only. It is not the final
canonical-main integration attestation.

## Acceptance matrix

| Row | Status | Direct evidence |
|---|---|---|
| A Process05 handoff | PASS | `internal/app/execution_runtime_test.go`; exact workspace digest in handoff |
| B VerificationSession durability | PASS | schema v83, restart and CAS tests |
| C–D criteria/critical claims | PASS | planner and completion mutation tests |
| E–F provenance/tree/clusters | PASS | exact binding and independent-cluster tests |
| G–H contradiction/staleness | PASS | decision matrix and regression invalidation test |
| I risk-scaled planning | PASS | planner rejects missing critical claims |
| J–K independent/meta-review | PASS | `TestReviewTheReviewerAndIndependenceRequired` |
| L–N adversarial/mutation/regression | PASS | mutation matrix, fuzz run, regression oracle tests |
| O security verification | PASS | required checks fail closed; full repository security tests |
| P real governed provider | NOT_RUN | no external provider credentials were used; provider configuration enforcement remains covered by repository integration tests |
| Q runtime negative proof | PASS | `NOT_RUN` and `UNKNOWN` block completion |
| R–S completion/replan | PASS | exact binding decision matrix |
| T bundle/postmortem | PASS | bundle payload/manifest tamper tests and postmortem builder |
| U surface parity | PASS | CLI/TUI/Web/MCP/A2A canonical status paths; protocol auth tests |
| V restart/budget | PASS | restart, concurrent CAS and budget exhaustion tests |
| W verifier security | PASS | caller binding/digest removal, sandbox policy and symlink tests |
| X full-chain E2E | PASS | `TestProcess05_FullChainE2E` and `TestProcess05_FailureChain_SafeReturn` in `internal/integration/process05_e2e_test.go` |
| Y performance/scale | PASS | 1,000-evidence benchmark |
| Z repository gates | PASS | candidate commands below |
| AA exact-main integration | PASS | `docs/process-06/INTEGRATION_ATTESTATION.md`, main `a53b4a2` |
| AB flaky evidence | PASS | mixed outcomes become quarantined `UNKNOWN` |
| AC oracle contamination | PASS | shared lineage is not independent |
| AD differential verification | PASS | disagreement becomes `UNKNOWN` |
| AE property/metamorphic | PASS | broken relation becomes `FAIL` |
| AF replay | PASS | output mutation produces `ErrReplayDivergence` |
| AG tamper-evident bundle | PASS | manifest, payload and binding tamper tests |
| AH external effects | PASS | authoritative read-back receipt mismatch fails |
| AI waivers | PASS | mandatory/critical evidence cannot be waived |
| AJ provider failover | PASS | only authorized config plus evidence is selectable |
| AK discovery | PASS | unbound discoveries remain `UNKNOWN` |
| AL calibration | PASS | false positive/negative rate gate |
| AM completion attestation | PASS | digest, version, bundle replay and applicability tests |
| AN semantic scope | PASS | empty/wildcard/traversal scope rejected |
| AO verifier sandbox | PASS | read-only/no-network/no-secret policy validation |
| AP integration attestation | PASS | `docs/process-06/INTEGRATION_ATTESTATION.md` |

## Candidate gates already observed

- `git diff --check`: PASS
- `go vet ./...`: PASS
- `go test ./...`: PASS
- `go test -race ./internal/verification ./internal/store ./internal/app ./internal/execution ./internal/cli`: PASS
- Process 06 fuzz, 10 seconds: PASS, 3,951,395 executions
- 1,000-evidence benchmark: PASS, 924,676 ns/op on the recorded host
- Web Vitest: PASS, 51 files / 118 tests
- Web production build: PASS
- Python conformance tests: PASS, 6 tests
- Python release tests: PASS, 8 tests
- Python v6 tests: PASS, 13 tests
- `conformance/runner.py validate-pack`: PASS
- Process 06 spec pack manifest: PASS, 51/51 files match SHA-256 and byte size
- `distribution/PACK-MANIFEST.json`: PASS after regeneration. The first
  candidate did not regenerate it, so CI release verification failed with
  sixty-one unlisted and mismatched paths. The spec pack manifest and the
  distribution manifest are different gates and both are now recorded.
- gitleaks over `origin/main..candidate`: PASS, no leaks
- `govulncheck ./...`: PASS, no vulnerabilities found
- `gosec ./...`: PASS for Process 06, 0 findings in the new verification code

  gosec reports 138 findings across 472 files repository-wide. None are in
  `internal/verification`, `internal/store/verification.go` or
  `internal/app/verification_runtime.go`. The 18 findings in files this
  candidate touched are pre-existing Process 05 patterns that the candidate
  reformatted but did not introduce: `G304` file-open-by-variable and
  `G301`/`G306` permission defaults in the execution engine, and one `G404`
  in `internal/execution/rate_limit.go:88`, which is retry-backoff jitter
  where `math/rand` is the correct choice. These are recorded as pre-existing
  repository findings, not Process 06 defects, and are out of scope for a
  process that must not patch implementation.

## Independent re-verification

The gates below were re-executed in a separate session against candidate
`3bbacea092fd97ef639a0b6671e65b8719be0558` with `origin/main` at
`a4a4f377630a9c9038dd6ebc3cd2d264773ed5cb`. They are recorded apart from the first
qualification run so the numbers are reproducible rather than inherited.

| Gate | Result |
|---|---|
| `go vet ./...` | PASS |
| `go test ./...` | PASS, 133 packages, 0 failures |
| `go test -race` on the five core packages | PASS |
| `TestProcess05_FullChainE2E` and failure chain | PASS |
| Process 06 fuzz, 10 seconds | PASS, 3,605,105 executions |
| 1,000-evidence benchmark | PASS, 729,180 ns/op on this host |
| Web Vitest | PASS, 51 files / 118 tests |
| Web production build | PASS |
| Spec pack manifest | PASS, 51/51 SHA-256 and byte size |
| `distribution/PACK-MANIFEST.json` | PASS after regeneration |
| gitleaks `origin/main..HEAD` | PASS, 14 commits, no leaks |
| `govulncheck ./...` | PASS, no vulnerabilities |
| gofmt on candidate-touched files | PASS after formatting schema 83 |
| `conformance/runner.py validate-pack` | PASS |
| Python conformance / release / v6 suites | PASS, 6 / 8 / 13 tests |

One record error from the first qualification was corrected: row X cited
`internal/app` rather than the integration package that actually holds the
full-chain test. The underlying evidence was re-run and does pass; only the
pointer was wrong.

A second correction was attempted and then withdrawn. The Python suites were
briefly recorded as NOT_APPLICABLE after a file search returned nothing; that
search had run from the wrong directory. The repository does contain Python
sources, CI runs them, and all three suites were re-run here and pass with the
counts originally recorded.

These results must be rerun where required after candidate integration. A PASS
here does not make the branch canonical main.
