# 03 — Baseline Audit (Evidence)

Status: **PASS**

## Frozen baseline
- Repository: github.com/Zen1th53/marshal
- Branch: `main`
- Baseline SHA: `7ceb9a7c7382d87eb9653d01ec90e0258ed7c30c`
- `git describe`: `v1.5.0-11-g7ceb9a7`
- Runtime spec version: `1.5.0` (RUNTIME-VERSION.yaml)
- Pack version: `6.0.0` (PACK-VERSION.yaml)
- Store schema version at baseline: **79** (`internal/store/migrations.go:LatestSchemaVersion`)
- Go toolchain: go1.27.0 linux/amd64
- Baseline `go build ./...`: exit 0
- Baseline `go vet ./internal/...`: exit 0

## Working tree note
At baseline the working tree carried pre-existing uncommitted TUI work (35 files,
`internal/tui/*`, `internal/sandbox/*`, `internal/a2a/*`, `internal/mcp/*` and
related tests). It is unrelated to Process 00. It was verified green
(`go test ./internal/tui/... ./internal/app/...` → ok) and left untouched.
Process 00 changes are kept to Process 00 files.

## Constitution search result
`grep -rl "onstitution" --include="*.go" internal/` → **0 files**.
No constitutional layer exists at baseline. Process 00 is a genuine new layer,
but it must compose existing primitives rather than replace them.

## Existing primitive inventory (candidates for reuse)
| Concern | Existing package | Verdict |
|---|---|---|
| Deterministic gate | `internal/gate` (`Engine.Evaluate`, `Decision`, `CheckResult`, stale-evidence + independent-verifier checks) | REUSE — compose, do not replace |
| Policy contracts/digest | `internal/policy` (`PolicyDigest`, engine, contracts) | REUSE |
| Risk classification | `internal/risk` (`Engine`, `AssessmentRequest`) | REUSE |
| Authorization | `internal/authz` (`Principal`, `Authority`, `CodeSelfApproval`, role bindings) | REUSE |
| Capability grants | `internal/capability` (broker, grants, revoke) | REUSE |
| Evidence | `internal/evidence` (nodes/edges, sanitizer, trust report, metrics) | REUSE |
| Provenance | `internal/provenance` | REUSE |
| Epistemic discipline | `internal/epistemic` (contradiction, coverage, revision, evidence discipline) | REUSE |
| Alignment | `internal/alignment` (blast radius, diff inspector, guard) | REUSE |
| Reinjection | `internal/reinjection` (compiler, digest, engine, guard) | REUSE — extend with constitution version |
| Goal contract | schema `goal_contracts` / `goal_active` (v73) with success criteria, constraints, do-not-do, critical claims | REUSE |
| Approvals | schema `approvals` (requested/approved/denied/expired/consumed/revoked, expiry, commit_hash) | REUSE — extend binding digest |
| Memory pipeline | `internal/app/memory_runtime.go` (`ExtractCandidate`→`Promote`, evidence-gated agent promotion), `memory_consolidation.go` | REUSE |
| Sandbox | `internal/sandbox` (bwrap) | REUSE |
| Network | `internal/netpolicy` (engine, authority, proxy) | REUSE |
| Secrets | `internal/secrets` (broker, leases) | REUSE |
| HCI | `internal/harness` (`Intelligence`, profiles, `AuditKnob`, `DetectDrift`) | REUSE — extend with governance states |
| Recommendations | `internal/recommendation` | REUSE |
| Checkpoint/rollback | schema `checkpoints`, `checkpoint_rollbacks`; `internal/recovery` | REUSE — verify truthfulness |
| Store/migrations | `internal/store` — sequential `if version == N` blocks, one transaction | REUSE convention |
| Surfaces | `internal/tui`, `internal/cli`, `internal/webcontrol`, `internal/mcp`, `internal/a2a` | Must route through the same authority |

## Genuine gaps (what Process 00 must add)
1. No typed, versioned **Constitution** entity, and no session binding to a version.
2. No **invariant registry** making constitutional rules machine-addressable.
3. No **authority hierarchy** resolver (Articles II/XI) — precedence is implicit.
4. No **decision envelope** unifying material decisions across surfaces.
5. No provider-neutral **Control Intelligence input/output contract** with a
   structured self-check, and no rule that AI output is advisory only.
6. No **constitutional violation engine** with the named violation classes.
7. No **completion policy** issuing VERIFIED from acceptance criteria, and no
   mechanically derived alignment score.
8. No constitutional **reason code** vocabulary.
9. HCI has no `VERIFIED_GOVERNED / DEGRADED / UNVERIFIED / UNAVAILABLE` states.

## Writers of success-bearing states (bypass survey)
Searched for emitters of PASS/VERIFIED/APPROVED/ROLLED_BACK/CLOSED/SANITIZED.
Gate decisions and approvals already flow through `internal/gate` and the store.
The Process 00 layer must ensure these remain the only issuers and that no
surface can mint them directly.
