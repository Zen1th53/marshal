# MARSHAL v1.0.1 — Canonical Community Consolidation and Production Hardening

v1.0.1 is a reconciliation and hardening release. It consolidates valid work
from overlapping branches onto the current Community runtime, removes false
production claims, and makes the existing release path deterministic and
verifiable.

## Highlights

- Canonical Community runtime and SQLite memory schema v72
- Governed memory consolidation, task-change cursors, retrieval quality gates,
  and current session importers
- Read-only Community Resource Awareness with bounded local/Ollama probes
- Fail-closed production Web boundary for fixture-only handlers
- Self-contained, symlink-safe clean initialization
- Pinned release dependencies/actions and synchronized licensing notices
- Rewritten user documentation grounded in v1.0.1 behavior
- Reproducible Linux amd64/arm64 archives, SPDX SBOM, checksums, release
  manifest, and GitHub build-provenance attestation

## Fixed

- `doctor` no longer executes optional provider probes unless
  `--probe-providers` is supplied.
- `marshal web serve` no longer hides runtime-open failures by serving demo
  state.
- Live Web routes backed only by fixtures now return `501 Not Implemented`.
- Legal source evidence reads `runtime_implementation_version` and
  `pack_version` from committed blobs instead of stale defaults.
- Clean `marshal init` can create the required policy/version defaults without
  a source checkout and rejects symlink replacements.

## Changed

- Community resource reporting includes CPU/cgroup, RAM/swap, storage,
  accelerator, thermal, and loopback Ollama awareness. Recommendations remain
  advisory and do not change runtime concurrency or policy.
- Community/Enterprise boundaries now explicitly exclude adaptive resource
  governors, fleet placement, and autonomous provider/model tuning.
- README and operator docs distinguish implemented adapters, local probes, and
  authenticated E2E verification.

## Verification

Final results will be recorded here after the release commit completes all
mandatory local and GitHub release gates.

| Gate | Result |
|---|---|
| Go build / vet / test / race | PENDING |
| Vulnerability scan | PENDING |
| Sandbox, policy, authz, memory, migration, resource, provider, backup tests | PENDING |
| Web install / typecheck / lint / test / build | PENDING |
| Python conformance and release tooling | PENDING |
| Clean install and first local workflow | PENDING |
| Pack manifest and release artifact verification | PENDING |

## Provider verification

| Provider | Adapter | Local binary probe | Authenticated E2E |
|---|---:|---|---|
| Codex | IMPLEMENTED | PENDING | NOT_RUN — external credentialed test not requested |
| OpenCode + Ollama | IMPLEMENTED | PENDING | NOT_RUN — OpenCode binary/service qualification unavailable unless explicitly enabled |
| Gemini CLI | IMPLEMENTED | PENDING | NOT_RUN — external credentialed test not requested |
| Claude Code | IMPLEMENTED | PENDING | NOT_RUN — external credentialed test not requested |

## Known limitations

- Endpoint-specific provider egress fails closed because Bubblewrap alone
  cannot enforce host/port allowlists and no enforcing proxy is wired.
- Bubblewrap is the supported production sandbox backend; process-only fallback
  is explicit and limited to eligible R0/R1 work.
- Fixture-only Web panels are disabled for live runtimes.
- No independent third-party security audit is claimed.
