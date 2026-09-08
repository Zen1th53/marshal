# Process 04 — Baseline Audit

Status: **PASS**

## Frozen baseline
- Baseline SHA: `1fdf85a452eeea949c54093443ce8729524ad386`
- Store schema: **81** · Constitution: **1.0.0** · Runtime spec: 1.5.0
- Go: go1.27.0 linux/amd64
- Pre-existing uncommitted TUI work present since Process 00, green, untouched.

## Existing primitives (reuse, do not duplicate)

| Concern | Existing | Verdict |
|---|---|---|
| Runtime task graph | `internal/dag` — persistent `Engine`, `Node`, `Edge`, status transitions, authorization | **DISTINCT** — see below |
| Candidate scoring / leasing | `internal/scheduler` — `Next`, capability matching, leases | **REUSE** at execution time; not plan-time |
| Goal contract + CAS | `model.GoalContract`, `store.SaveGoalContract` | **REUSE** — the plan's input |
| Confirmation gating | `model.ConfirmationState.Settled()` | **REUSE** — the handoff precondition |
| Risk dimensions | `goalintake.Assessment` (ten independent axes) | **REUSE** — plan inherits, never recomputes |
| Provider selection | `goalintake.Select` — governance above capacity | **REUSE** — routing is the same decision |
| Capacity honesty | `goalintake.Capacity` — UNKNOWN never a number | **REUSE** |
| Harness governance | `harness.AssessGovernance` (Process 00) | **REUSE** |
| Project identity / scope | `internal/projectid` (Process 02) | **REUSE** |
| Constitutional gate | `constitution.Evaluate`, `GatePromotion` | **REUSE** |
| Approvals | `store.approvals`, `constitution` binding digest | **REUSE** — plan records requirements only |

### Why a separate plan-time DAG

`internal/dag` is a *runtime* graph: nodes carry live status
(`NodeStatus`), transitions are authorized, and mutations are persisted
through a backend. It models work that is happening.

Process 04 needs to reason about work that has **not** started — ordering,
critical path, parallel-safety, collision detection — before any node exists at
runtime. Reusing the runtime engine would mean creating live nodes during
planning, which is precisely the worker-execution boundary Process 04 must not
cross.

So the plan-time DAG is a separate, in-memory structure that produces an
`ExecutionPlan`; Process 05 is what turns that into runtime nodes.

## Gaps

1. **No `ExecutionPlan`** — no type, no table, no persistence.
2. **No plan-time task decomposition** or Goal-to-task traceability.
3. **No plan-time DAG** with critical path or collision detection.
4. **No team assembly** — roles exist (`orchestrator`, `architect`,
   `developer`, `qa`, `appsec` in the schema's role CHECK) but nothing selects
   a minimum sufficient set.
5. **No verification obligation planning**, so nothing can enforce that a
   critical criterion has a verification path before a plan is READY.
6. **No Process 05 handoff gate.**

## Design conclusion

Build `internal/plan` producing a canonical `ExecutionPlan`, persisted with CAS
alongside the Goal it derives from. Every risk figure is inherited from the
Process 03 assessment rather than recomputed, so complexity and risk stay
separate and no second opinion can drift from the first.
