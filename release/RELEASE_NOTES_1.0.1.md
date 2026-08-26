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
- Repeated provider capability checks no longer collide in the durable audit
  stream; each decision retains fail-closed audit enforcement.
- Network-required provider runs now return `NET_ENFORCEMENT_UNAVAILABLE`
  instead of opening a Bubblewrap network namespace that could bypass the
  endpoint allowlist proxy.
- The release toolchain now uses Go 1.25.13, which clears the reachable
  standard-library vulnerabilities reported against Go 1.25.0.

## Changed

- Community resource reporting includes CPU/cgroup, RAM/swap, storage,
  accelerator, thermal, and loopback Ollama awareness. Recommendations remain
  advisory and do not change runtime concurrency or policy.
- Community/Enterprise boundaries now explicitly exclude adaptive resource
  governors, fleet placement, and autonomous provider/model tuning.
- README and operator docs distinguish implemented adapters, local probes, and
  authenticated E2E verification.

## Verification

The mandatory Community release gate ran on 2026-08-26 with Go 1.25.13.

| Gate | Result |
|---|---|
| Go build / vet / test / race | PASS |
| `govulncheck ./...` | PASS — no vulnerabilities found |
| Sandbox, policy, authz, memory, migration, resource, provider, backup tests | PASS |
| Web `npm ci` / typecheck / lint / 116 tests / build / embedded-asset parity | PASS |
| Python pack conformance, release tooling, and legacy tooling | PASS |
| Clean install, initialization, doctor, daemon, first local workflow, backup, Web start, restart persistence | PASS |
| Source pack manifest | PASS |
| Reproducible archives, SPDX SBOM, checksums, and release-manifest tests | PASS |

ESLint emitted one non-fatal `no-useless-escape` warning in
`web/src/api/errors.ts`; lint still exited successfully. Credentialed provider
qualification is reported separately below and is not included in the
mandatory gate PASS.

## Provider verification

| Provider path | Adapter/probe | Adapter/model E2E | Canonical Runtime E2E |
|---|---|---|---|
| Codex | PASS — local `codex-cli 0.149.1` | NOT_RUN — credentialed execution not enabled | NOT_RUN |
| OpenCode + DeepSeek V4 | PASS — workstation OpenCode 1.18.16 | PASS — Flash 7.01s and Pro 6.64s; strict proof rerun also PASS | NOT_RUN — endpoint-enforcing provider egress unavailable |
| OpenCode + Ollama | PASS — workstation Ollama 0.32.9 and model inventory; release host service unavailable | FAIL — `qwythos-9b` and `blackarch-ai` wrote incorrect proof content; `qwen2.5-coder-abliterate:14b` created no proof file | NOT_RUN — endpoint-enforcing provider egress unavailable |
| Gemini CLI | NOT_AVAILABLE — optional binary absent | NOT_RUN — binary and credentialed execution unavailable | NOT_RUN |
| Claude Code | PASS — local Claude Code 2.1.198 | NOT_RUN — credentialed execution not enabled | NOT_RUN |

The OpenCode model qualification ran on commit `b37b187` in an isolated
temporary repository. Process exit zero was insufficient: the test required
the requested proof file and exact content. No local-model failure is reported
as PASS.

## Known limitations

- Endpoint-specific provider egress fails closed because Bubblewrap alone
  cannot enforce host/port allowlists and no enforcing proxy is wired.
- Bubblewrap is the supported production sandbox backend; process-only fallback
  is explicit and limited to eligible R0/R1 work.
- Fixture-only Web panels are disabled for live runtimes.
- No independent third-party security audit is claimed.
