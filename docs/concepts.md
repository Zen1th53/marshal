# Concepts

These definitions summarize the repository contracts.

- **Task:** A versioned unit of work with status, risk, dependencies, owner,
  base/HEAD commits, branch, and worktree.
- **Lease:** A transactional, time-bounded claim binding one task to one
  session. Expiry alone does not authorize silent task theft.
- **Agent:** A registered project identity with a fixed role, capabilities,
  provider metadata, status, and revision.
- **Session:** A persisted execution identity binding an agent, project, role,
  optional task, branch, worktree, liveness, and lifecycle status.
- **Role:** One of Orchestrator, Architect, Developer, QA, or AppSec, with
  authority defined by [TEAM.md](../TEAM.md).
- **Finding:** A QA- or AppSec-owned issue with severity, evidence, lifecycle,
  and revision. Developers cannot self-close another role's finding.
- **Decision:** A durable, role-owned engineering conclusion with status,
  source, body, and revision.
- **Approval:** Contextual authorization for a dangerous operation, bound to
  operation, scope, target, commit, expiry, status, and revision.
- **Checkpoint:** Recoverable task/session state tied to an exact repository
  commit.
- **Handoff:** A durable transfer of scoped context and responsibility from
  one role/session to another.
- **Artifact:** Immutable output identified by digest and bound to source
  commit, producer session, tasks, and verification references.
- **Evidence:** Reproducible observation supporting a claim. Agent prose alone
  is not verification evidence.
- **Memory:** Provenance-bearing shared state whose authority, confidence,
  staleness, and owner are explicit.
- **Adapter:** A translation layer between a native agent's lifecycle and the
  SLAVES adapter contract.
- **Worker:** A task-scoped process managed through prepare, run, heartbeat,
  checkpoint, verify, release, and exit states.
- **Runtime:** The executable enforcement plane for identity, policy, leases,
  workers, events, artifacts, and canonical coordination state.
- **Conformance Scenario:** A static or executable case that tests an explicit
  control-plane invariant and records `PASS`, `FAIL`, `BLOCKED`, or `SKIP`.

For machine fields, see [runtime/SCHEMA.yaml](../runtime/SCHEMA.yaml) and
[schemas/](../schemas/README.md).
