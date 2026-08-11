# Agent Runtime Plane Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Define the runtime contracts needed to enforce the existing multi-agent engineering pack through an executable control plane.

**Architecture:** Start with a local single-daemon design backed by SQLite and filesystem worktrees/artifacts. Keep MCP/API, semantic indexes, graph indexes, external secret managers, and distributed workers behind adapters.

**Tech Stack:** Runtime-contract Markdown/YAML specifications; future implementation should prefer a small local daemon, SQLite for canonical state, Git worktrees for isolation, and repository-native CI.

## Global Constraints

- No mandatory cloud service.
- Policy check before privileged execution.
- Canonical state is structured and transactional.
- Semantic/history indexes are derived state.
- Secrets never enter general memory.
- Artifacts are immutable by digest.
- Worker completion requires evidence, not heartbeat disappearance.
- Runtime remains agent/model agnostic.

---

### Task 1: Runtime architecture and CLI

**Files:**
- Create: `runtime/README.md`
- Create: `runtime/ARCHITECTURE.md`
- Create: `runtime/AGENTCTL.md`

- [x] Define runtime modes and component boundaries.
- [x] Define `agentctl` command surface.
- [x] Define status and error semantics.

### Task 2: Canonical memory/control-plane service

**Files:**
- Create: `runtime/MEMORY-SERVICE.md`
- Create: `runtime/SCHEMA.yaml`

- [x] Define canonical entities and transactional semantics.
- [x] Define adapter boundary for vector/graph/session retrieval.

### Task 3: Agent identity and worker execution

**Files:**
- Create: `runtime/IDENTITY-REGISTRY.md`
- Create: `runtime/WORKER-PROTOCOL.md`
- Create: `runtime/SANDBOX.md`

- [x] Define session identity, heartbeat, capabilities, task binding, sandbox contract.

### Task 4: Policy and approvals

**Files:**
- Create: `runtime/POLICY-ENGINE.md`

- [x] Define authorization input/output.
- [x] Bind capability, role, task, approval, environment, and operation.

### Task 5: Scheduling and events

**Files:**
- Create: `runtime/SCHEDULER.md`
- Create: `runtime/EVENT-BUS.md`
- Create: `runtime/EVENTS.yaml`

- [x] Define task-ready rules, leases, retry/liveness behavior, event schemas.

### Task 6: Secrets and artifacts

**Files:**
- Create: `runtime/SECRETS-BROKER.md`
- Create: `runtime/ARTIFACT-STORE.md`

- [x] Define short-lived scoped secret leases.
- [x] Define content-addressed immutable artifact records.

### Task 7: Runtime operations

**Files:**
- Create: `runtime/HEALTH.md`
- Create: `runtime/THREAT-MODEL.md`
- Create: `runtime/IMPLEMENTATION-ROADMAP.md`

- [x] Define health, recovery, security boundary, and staged implementation.

### Task 8: Pack integration

- [x] Extend manifest routing.
- [x] Link TEAM/ORCHESTRATOR/README/FINAL-PACK.
- [x] Add runtime version metadata.

### Task 9: Verification

- [x] Verify required runtime files.
- [x] Verify internal references.
- [x] Verify manifest references.
- [x] Verify no unfinished placeholders.
- [x] Package and hash ZIP.
