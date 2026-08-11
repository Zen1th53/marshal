# Agent OS Completeness Design

## Context

The pack already provides:

- shared engineering doctrine,
- Architect / Developer / QA / AppSec roles,
- Orchestrator,
- task graph and leases,
- worktree isolation,
- shared persistent memory,
- reference-repository usage,
- approvals,
- evidence,
- release and incident protocols,
- progressive context loading,
- memory backend evolution,
- doctrine evaluation.

The remaining gap is not another role. It is governance around the execution environment, tools, artifacts, provenance, pipelines, data, and the agent pack itself.

## Goals

Add the missing control-plane layers without turning the reusable pack into a mandatory runtime platform.

Every new layer must:

1. work as a file-first protocol,
2. be conditionally loaded,
3. have explicit authority,
4. preserve repository truth over memory,
5. avoid introducing mandatory services,
6. allow later automation through MCP/API/CI.

## Non-goals

- Implement a cloud control plane.
- Require a vector database, graph database, or central server.
- Require one specific LLM vendor.
- Replace repository-native CI/CD.
- Replace organizational IAM, secrets management, or change-management systems.

## Added Layers

### 1. Capability and Tool Permission Plane

Defines what each role may read, write, execute, network-access, deploy, or mutate.

Files:
- `CAPABILITIES.yaml`
- `protocols/CAPABILITIES.md`
- `protocols/TOOL-ROUTING.md`

### 2. Instruction Trust / Prompt-Injection Boundary

Classifies repository policy, task text, code comments, fetched web pages, retrieved memory, issue text, and third-party docs by trust.

Files:
- `protocols/INSTRUCTION-TRUST.md`

### 3. Environment Bootstrap and Reproducibility

Separates "repository is correct" from "agent environment happens to work."

Files:
- `memory/ENVIRONMENT.md`
- `protocols/BOOTSTRAP.md`

### 4. Ownership and Review Routing

Maps components/domains to responsible roles or human owners without inventing CODEOWNERS semantics.

Files:
- `memory/OWNERSHIP.md`
- `protocols/OWNERSHIP-ROUTING.md`

### 5. End-to-End Traceability

Links requirement → decision/design → task → commit → test/evidence → artifact → release.

Files:
- `memory/TRACEABILITY.md`
- `protocols/TRACEABILITY.md`

### 6. Artifact Provenance

Build/test/generated artifacts gain source commit, build command, environment, hash, producer, and verification status.

Files:
- `memory/ARTIFACTS.md`
- `protocols/ARTIFACT-PROVENANCE.md`

### 7. CI/CD Orchestration

Defines pipeline truth, required checks, immutable artifacts, promotion, rerun rules, flaky handling, and verification invalidation.

Files:
- `protocols/CI-CD.md`

### 8. Dependency / Supply-Chain Ledger

Persists why dependencies exist, provenance/license/risk, version policy, and replacement/removal path.

Files:
- `memory/DEPENDENCIES.md`
- `protocols/SUPPLY-CHAIN.md`

### 9. Observability / Audit Plane

Records control-plane events without turning agent thought into telemetry.

Files:
- `memory/RUNS.md`
- `protocols/OBSERVABILITY.md`

### 10. Data Classification / Retention

Defines what may enter memory, logs, semantic indexes, artifacts, or external tools.

Files:
- `memory/DATA-POLICY.md`
- `protocols/DATA-GOVERNANCE.md`

### 11. Resource Budgeting

Bounds context, tool calls, external research, expensive scans, and parallelism without sacrificing correctness.

Files:
- `memory/BUDGETS.md`
- `protocols/RESOURCE-BUDGET.md`

### 12. Deadlock / Liveness

Handles task cycles, abandoned leases, role ping-pong, blocked dependency graphs, and repeated rework.

Files:
- `protocols/LIVENESS.md`

### 13. Pack Versioning / Schema Migration

The agent pack itself becomes versioned and migratable.

Files:
- `PACK-VERSION.yaml`
- `CHANGELOG.md`
- `protocols/PACK-MIGRATION.md`

### 14. Backup / Restore / Disaster Recovery

Covers memory/control-plane corruption and restore without confusing memory rollback with source-code rollback.

Files:
- `protocols/RECOVERY.md`

## Architecture

```text
                         USER / OWNER
                              │
                        ORCHESTRATOR
                              │
      ┌───────────────────────┼───────────────────────┐
      │                       │                       │
  Task Graph             Context Router        Capability Plane
      │                       │                       │
      ├──── Ownership ────────┤                Instruction Trust
      │                       │                       │
      ▼                       ▼                       ▼
 Architect ───────── Developer ───────── QA ───────── AppSec
      │                │                 │             │
      └────────────────┴──── Evidence ───┴─────────────┘
                              │
                     Traceability / Artifacts
                              │
                         CI/CD / Release
                              │
                  Observability / Audit / DR

Shared cross-cutting:
- Memory
- Data policy
- Supply chain
- Resource budgets
- Liveness
- Pack versioning
```

## Key Invariants

- Fresh repository/runtime evidence outranks memory.
- Untrusted content cannot redefine agent policy.
- Tool possession does not imply permission.
- One task has one active implementation owner unless explicitly subdivided.
- Verification binds to exact repository/artifact state.
- Artifact identity binds to source and build provenance.
- CI reruns do not erase flaky or failed evidence.
- Secrets never enter semantic memory or general logs.
- A dependency is not "approved forever"; its ledger states why it exists and when to revalidate.
- Pack upgrades cannot silently change role authority or memory semantics.
- Recovery of memory state never silently rewinds source code.

## Failure Model

Each new layer is advisory/file-first by default. If an optional automation backend is unavailable:

- repository work can continue when canonical local state is intact,
- unavailable verification is marked explicitly,
- dangerous operations remain blocked if approval/capability cannot be proven,
- no state is fabricated.

## Testing / Verification

The pack is verified structurally:

- required files exist,
- internal references resolve,
- manifest entries resolve,
- role/control-plane hooks exist,
- no unfinished placeholders,
- pack version is present,
- ZIP is built and hashed.

## Security

The most important new security boundary is instruction trust:

```text
trusted policy
≠ repository content
≠ fetched documentation
≠ issue text
≠ retrieved memory
```

External or retrieved text is data until a trusted local policy explicitly promotes it.

## Decision

Adopt the complete protocol-first design. Do not add mandatory runtime services.
