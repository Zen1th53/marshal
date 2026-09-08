# Process 06 implementation

Process 06 is implemented as a fail-closed review and completion boundary. An
agent claim is never sufficient to complete a Goal. The runtime reloads the
canonical Goal, Plan, ExecutionRun and workspace tree before every decision.

The implementation consists of:

- `internal/verification`: exact-state bindings, criteria and critical claims,
  evidence lineage/clusters, contradictions, staleness, planning, independent
  review, flaky evidence quarantine, differential and metamorphic checks,
  replay, tamper-evident bundles, external-effect receipts, waivers, governed
  provider failover, discovery, calibration, mutation isolation, budgets,
  completion and integration attestations;
- `internal/store`: schema v83 durable verification sessions with CAS and
  append-only completion attestations;
- `internal/app`: canonical Process 06 service. Callers cannot supply the
  supposedly current binding and cannot attest a caller-provided digest;
- `internal/execution`: Process 05 handoffs now contain a deterministic digest
  of the actual workspace, including dirty and untracked files and excluding
  `.git` and `.marshal`. Symlinks are not followed and concurrent file mutation
  aborts digest creation;
- CLI, TUI, Web, MCP and A2A surfaces that display canonical durable state.
  Mutating completion operations remain behind the runtime service.

Completion requires every mandatory criterion, critical evidence from at least
two independent clusters, PASS for every required runtime/security check, no
unresolved critical contradiction, current evidence, and exact
Goal/Plan/Run/tree/environment agreement. `NOT_RUN`, `UNKNOWN`, missing evidence
and stale bindings cannot become `VERIFIED_COMPLETE`.

The formal completion attestation is immutable, versioned, digest-protected and
bounded to one exact state. The service verifies every evidence-bundle payload
against its manifest before writing the attestation.
