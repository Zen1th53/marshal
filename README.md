# SLAVES

**Structured Lifecycle for Agent Verification, Execution & Supervision**

A vendor-neutral engineering control plane for multi-agent AI development,
verification, security, memory, orchestration, and runtime governance.

This pack is intentionally technology-agnostic. It is built around six rules:

1. **No guessing when the repository can answer.**
2. **Get the data structures and invariants right before the code.**
3. **No coding before the problem and blast radius are understood.**
4. **No unrelated changes.**
5. **No claim without evidence.**
6. **No agent approves its own work outside its authority.**

`TORVALDS.md` is the shared engineering doctrine. It is not a role; it governs engineering judgment across all roles.

`ORCHESTRATOR.md` is the coordination role. It discovers whether a task exists, asks the user when it does not, assigns Architect/Developer/QA/AppSec by risk, and enforces handoff/evidence gates.

The style is deliberately terse and engineering-first: small diffs, explicit invariants, strong review boundaries, measurable verification, no ceremonial process.

## Layout

```text
agents/
├── TEAM.md
├── TORVALDS.md
├── ORCHESTRATOR.md
├── ARCHITECT.md
├── DEVELOPER.md
├── QA.md
├── APPSEC.md
├── protocols/
│   ├── HANDOFF.md
│   ├── REVIEW.md
│   ├── DEBUGGING.md
│   ├── EVIDENCE.md
│   ├── RELEASE.md
│   └── INCIDENT.md
└── templates/
    ├── DESIGN.md
    ├── ADR.md
    ├── TEST-PLAN.md
    ├── SECURITY-REVIEW.md
    ├── THREAT-MODEL.md
    ├── BUG-REPORT.md
    └── RELEASE-CHECKLIST.md
```

## Recommended repository placement

```text
repo/
├── AGENTS.md              # project-specific rules
├── SPEC.md                # project-specific requirements
├── agents/                # this pack
└── ...
```

`AGENTS.md` should point each worker to `agents/TEAM.md` and its own role file.

## Project-specific facts belong elsewhere

Do not edit these reusable role files to encode one project's framework, package manager, URL structure, vendor, or deployment topology.

Keep project-specific facts in repository-local sources such as:

- `AGENTS.md`
- `SPEC.md`
- `CONTRIBUTING.md`
- `SECURITY.md`
- architecture docs
- ADRs
- CI configuration
- package/build files

## Suggested orchestrator mapping

```text
architecture/design request      → ARCHITECT
implementation/bugfix            → DEVELOPER
verification/release confidence  → QA
security review/threat model     → APPSEC
```

For security-sensitive changes:

```text
ARCHITECT ↔ APPSEC → DEVELOPER → QA + APPSEC → final gate
```

## Hard rule

If the role file conflicts with an explicit repository rule, the repository rule wins unless it is unsafe, impossible, or contradictory. Surface the conflict instead of silently choosing.

## Required reading order

For non-trivial work:

```text
1. Repository AGENTS.md / local policy
2. TEAM.md
3. TORVALDS.md
4. ORCHESTRATOR.md (when coordinating the team)
5. Assigned role file
6. Relevant protocol(s)
7. Project spec / ADR / task context
```

`TEAM.md` governs authority and coordination.  
`TORVALDS.md` governs engineering judgment and patch quality.  
The assigned role file governs role-specific execution.


## Shared persistent team memory

The pack includes a file-first, backend-agnostic memory layer:

```text
memory/
├── STATE.md
├── MEMORY.md
├── DECISIONS.md
├── FINDINGS.md
├── HANDOFFS.md
├── CHECKPOINTS.md
├── SCHEMA.md
└── REFERENCES.md
```

The initial version requires no memory server.

Later optional adapters can add:

- Deja Vu — historical cross-agent session recall,
- TurboVec — local semantic/vector index,
- Cognee — graph/vector shared knowledge,
- Memoria — versioning/snapshot/rollback patterns,
- TencentDB Agent Memory — layered/progressive disclosure,
- claude-remember — persistent session/bootstrap/consolidation patterns.

The core remains agent-agnostic.

Read `protocols/MEMORY.md` for authority, staleness, conflict, retrieval, and write rules.


## External reference usage

`protocols/REFERENCE-USE.md` governs how agents use repositories listed in `memory/REFERENCES.md`.

The protocol requires:

```text
local repo first
→ relevant reference only
→ upstream verification
→ pattern extraction
→ local-fit comparison
→ smallest integration
→ tests/evidence
```

References are not automatically installed, cloned, or adopted as dependencies.

---

## Multi-Agent Control Plane

The pack now includes:

```text
memory/TASKS.md
protocols/TASK-CONTROL.md
protocols/WORKTREE.md
AGENT-MANIFEST.yaml
protocols/CONTEXT-LOADING.md
memory/BACKEND.md
protocols/MEMORY-BACKEND.md
memory/APPROVALS.md
protocols/APPROVAL.md
EVALS.md
```

This adds task ownership, parallel work isolation, progressive context loading, backend evolution, dangerous-operation approvals, and agent-drift evaluation.

---

## Final Control-Plane Completion

Ultimate V3 adds the remaining reusable governance layers:

- capability/tool permissions,
- instruction-trust / prompt-injection isolation,
- reproducible environment bootstrap,
- component ownership/review routing,
- requirement-to-release traceability,
- artifact provenance,
- CI/CD orchestration,
- durable dependency/supply-chain ledger,
- run observability/audit,
- data classification/retention,
- resource budgets,
- liveness/deadlock handling,
- pack versioning/migrations,
- backup/restore/disaster recovery.

These are protocol-first and conditionally loaded. No new mandatory runtime service is introduced.

---

## Executable Runtime Specification

`runtime/` contains the executable-control-plane contracts for:

- `agentctl`,
- canonical state service,
- identity/heartbeat,
- policy engine,
- worker/sandbox manager,
- scheduler,
- event bus,
- secrets broker,
- artifact store.

`RUNTIME-VERSION.yaml` explicitly marks this as a specification rather than a
claim that a production daemon has already been implemented.

The recommended first real implementation is:

```text
local daemon + SQLite + Git worktrees + filesystem artifacts
```

before any distributed infrastructure.

---

## Universal Agent Adapters and Conformance

Ultimate V5 adds:

- Gemini CLI adapter,
- Codex adapter,
- Claude Code adapter,
- OpenCode adapter,
- Aider worker adapter,
- Crush adapter,
- machine-readable compatibility matrix,
- official/upstream source snapshot,
- 18 adversarial conformance scenarios,
- executable static conformance runner,
- executable project/state helper,
- project bootstrap,
- file/runtime reconciliation,
- capability-based model routing,
- distribution/self-upgrade protocol.

The shared core remains vendor-neutral.

---

## Ultimate V6 — Final Portable Control Layers

V6 closes the remaining specification/control gaps:

- A2A 1.0 remote agent interoperability,
- MCP 2026-07-28 tool/context profile,
- JSON Schema 2020-12 machine contracts,
- OpenTelemetry-oriented telemetry,
- SLSA/in-toto release provenance design,
- detached Ed25519 sign/verify helper,
- live behavioral conformance runner,
- 26 adversarial/fault scenarios,
- plugin compatibility/governance,
- multi-tenant isolation,
- reproducible packaging rules.

`release/TRUST-STATUS.json` deliberately says `UNSIGNED_BY_OWNER`; the pack does
not manufacture a fake trust root.
