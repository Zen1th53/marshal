# FINAL-PACK.md

## Purpose

Reusable four-agent engineering team pack with shared engineering doctrine.

## Mandatory core

- `TEAM.md` — authority, coordination, risk classes, handoffs, gates.
- `TORVALDS.md` — shared engineering doctrine and patch-quality rules.
- `ORCHESTRATOR.md` — task discovery, role routing, workflow coordination, and final evidence gate.
- `ARCHITECT.md` — architecture role operating system.
- `DEVELOPER.md` — implementation/debugging role operating system.
- `QA.md` — independent verification role operating system.
- `APPSEC.md` — application-security role operating system.

## Shared protocols

- `protocols/HANDOFF.md`
- `protocols/REVIEW.md`
- `protocols/DEBUGGING.md`
- `protocols/EVIDENCE.md`
- `protocols/RELEASE.md`
- `protocols/INCIDENT.md`

## Reusable templates

- `templates/DESIGN.md`
- `templates/ADR.md`
- `templates/TEST-PLAN.md`
- `templates/SECURITY-REVIEW.md`
- `templates/THREAT-MODEL.md`
- `templates/BUG-REPORT.md`
- `templates/RELEASE-CHECKLIST.md`

## Reading order

```text
Repository policy
→ TEAM.md
→ TORVALDS.md
→ ORCHESTRATOR.md (when coordinating)
→ assigned role
→ relevant protocols
→ project spec/ADR/task
```


## Shared Memory

- `protocols/MEMORY.md`
- `memory/STATE.md`
- `memory/MEMORY.md`
- `memory/DECISIONS.md`
- `memory/FINDINGS.md`
- `memory/HANDOFFS.md`
- `memory/CHECKPOINTS.md`
- `memory/SCHEMA.md`
- `memory/REFERENCES.md`
- `templates/MEMORY-HANDOFF.md`
- `templates/CHECKPOINT.md`
- `MEMORY-START.md`

## Reference Repository Protocol

- `protocols/REFERENCE-USE.md`

---

## Multi-Agent Control Plane

- `memory/TASKS.md`
- `protocols/TASK-CONTROL.md`
- `protocols/WORKTREE.md`
- `AGENT-MANIFEST.yaml`
- `protocols/CONTEXT-LOADING.md`
- `memory/BACKEND.md`
- `protocols/MEMORY-BACKEND.md`
- `memory/APPROVALS.md`
- `protocols/APPROVAL.md`
- `EVALS.md`
- `templates/TASK.md`
- `templates/APPROVAL.md`
- `templates/EVAL-REPORT.md`

---

## Ultimate V3 — Remaining Layers

- `CAPABILITIES.yaml`
- `protocols/CAPABILITIES.md`
- `protocols/TOOL-ROUTING.md`
- `protocols/INSTRUCTION-TRUST.md`
- `memory/ENVIRONMENT.md`
- `protocols/BOOTSTRAP.md`
- `memory/OWNERSHIP.md`
- `protocols/OWNERSHIP-ROUTING.md`
- `memory/TRACEABILITY.md`
- `protocols/TRACEABILITY.md`
- `memory/ARTIFACTS.md`
- `protocols/ARTIFACT-PROVENANCE.md`
- `protocols/CI-CD.md`
- `memory/DEPENDENCIES.md`
- `protocols/SUPPLY-CHAIN.md`
- `memory/RUNS.md`
- `protocols/OBSERVABILITY.md`
- `memory/DATA-POLICY.md`
- `protocols/DATA-GOVERNANCE.md`
- `memory/BUDGETS.md`
- `protocols/RESOURCE-BUDGET.md`
- `protocols/LIVENESS.md`
- `PACK-VERSION.yaml`
- `CHANGELOG.md`
- `protocols/PACK-MIGRATION.md`
- `protocols/RECOVERY.md`

---

## Executable Runtime Specification

- `RUNTIME-VERSION.yaml`
- `runtime/README.md`
- `runtime/ARCHITECTURE.md`
- `runtime/MARSHAL-CLI.md`
- `runtime/MEMORY-SERVICE.md`
- `runtime/IDENTITY-REGISTRY.md`
- `runtime/POLICY-ENGINE.md`
- `runtime/SANDBOX.md`
- `runtime/WORKER-PROTOCOL.md`
- `runtime/SCHEDULER.md`
- `runtime/EVENT-BUS.md`
- `runtime/EVENTS.yaml`
- `runtime/SECRETS-BROKER.md`
- `runtime/ARTIFACT-STORE.md`
- `runtime/HEALTH.md`
- `runtime/THREAT-MODEL.md`
- `runtime/SCHEMA.yaml`
- `runtime/IMPLEMENTATION-ROADMAP.md`

---

## Ultimate V5 — Adapters and Conformance

- `AGENT-BOOTSTRAP.md`
- `adapters/README.md`
- `adapters/CONTRACT.md`
- `adapters/MATRIX.json`
- `adapters/COMPATIBILITY.md`
- `adapters/UPSTREAM-SOURCES.md`
- `adapters/gemini/`
- `adapters/codex/`
- `adapters/claude-code/`
- `adapters/opencode/`
- `adapters/aider/`
- `adapters/crush/`
- `conformance/README.md`
- `conformance/CONTRACT.md`
- `conformance/ADVERSARIAL.md`
- `conformance/SCENARIOS.json`
- `conformance/runner.py`
- `conformance/fixtures/`
- `bootstrap/`
- `protocols/RECONCILIATION.md`
- `runtime/RECONCILIATION.md`
- `routing/`
- `distribution/`
- `protocols/SELF-UPGRADE.md`
- `tools/marshal.py`

---

## Ultimate V6 — Final Layers

- `schemas/`
- `interop/`
- `telemetry/`
- `plugins/`
- `tenancy/`
- `standards/REFERENCES.md`
- `release/`
- `protocols/INTEROP.md`
- `protocols/PROTOCOL-VERSIONING.md`
- `protocols/TELEMETRY.md`
- `protocols/PLUGIN-GOVERNANCE.md`
- `protocols/TENANCY.md`
- `protocols/RELEASE-TRUST.md`
- `protocols/REPRODUCIBILITY.md`
- `conformance/behavioral_runner.py`
- `conformance/FAULT-INJECTION.md`
- `tools/schema_validate.py`
- `tools/protocol_check.py`
- `tools/release_verify.py`
- `tools/detached_sign.py`
