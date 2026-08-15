# Architecture

MARSHAL separates durable engineering authority from the coding-agent vendor
that performs work. This page is a map; the linked specifications remain
canonical.

```text
User
  │
Orchestrator ── role authority ── Architect / Developer / QA / AppSec
  │
Task graph ── ownership + lease ── Agent session
  │
Policy + approval
  │
Adapter ── Worker ── Worktree / sandbox
  │
Events ── Artifacts ── Evidence ── Review gates
```

## Control plane and roles

[TEAM.md](../TEAM.md) defines authority. The
[Orchestrator](../ORCHESTRATOR.md) coordinates work but cannot replace
Architect, QA, or AppSec verdicts. [TORVALDS.md](../TORVALDS.md) defines shared
engineering judgment.

## Tasks, ownership, and leases

Tasks form a dependency graph. A ready implementation task has one active
owner, branch, worktree, session, and lease. SQLite Runtime V1 enforces claims
transactionally. See [TASK-CONTROL.md](../protocols/TASK-CONTROL.md),
[WORKTREE.md](../protocols/WORKTREE.md), and
[SCHEDULER.md](../runtime/SCHEDULER.md).

## Memory and decisions

File-first memory remains a human-readable interchange and checkpoint layer.
In local runtime mode, SQLite is canonical for live coordination. Provenance,
staleness, ownership, and reconciliation rules are defined in
[memory/MEMORY.md](../memory/MEMORY.md),
[memory/BACKEND.md](../memory/BACKEND.md), and
[runtime/RECONCILIATION.md](../runtime/RECONCILIATION.md).

## Policy and approvals

Capabilities describe semantic operations, not command spelling. Policy
returns `ALLOW`, `DENY`, or `REQUIRE_APPROVAL`; approvals bind dangerous
operations to scope, target, commit, status, and expiry. See
[CAPABILITIES.yaml](../CAPABILITIES.yaml),
[POLICY-ENGINE.md](../runtime/POLICY-ENGINE.md), and
[APPROVAL.md](../protocols/APPROVAL.md).

The implemented T48 lifecycle, runtime boundary, metrics, and recovery
semantics are documented in [Policy-as-Code](policy-as-code.md).

## Runtime, workers, and adapters

The local daemon owns canonical state and exposes a local Unix-socket API.
Workers are task- and worktree-scoped. Adapters normalize native agent
lifecycle and evidence without granting role authority. Runtime `v0.4.0` implements Codex and OpenCode + local Ollama execution; the other adapters have defined contracts. See
[runtime/README.md](../runtime/README.md) and
[adapters/CONTRACT.md](../adapters/CONTRACT.md).

## Conformance, evidence, and provenance

Static validation checks the pack; behavioral and adversarial scenarios test
observable invariants. Artifacts use digest identity and verification binds to
an exact commit. See [conformance/README.md](../conformance/README.md),
[EVIDENCE.md](../protocols/EVIDENCE.md), and
[ARTIFACT-PROVENANCE.md](../protocols/ARTIFACT-PROVENANCE.md).

## Interoperability

[interop/](../interop/README.md) specifies A2A and MCP negotiation boundaries.
They are contracts in Runtime V1, not deployed remote servers. Remote agents
remain external principals until local policy establishes identity and scope.
