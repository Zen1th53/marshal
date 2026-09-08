# 03/04 — Process 03 Baseline Audit and Request Entrypoint Map

Status: **PASS** (audit complete; gaps recorded)

## Frozen baseline
- Baseline SHA: `3e06a20a0dc6d7002c7498c0e79a461ea7de052b`
- Store schema: **80** · Constitution: **1.0.0** · Runtime spec: 1.5.0
- Go: go1.27.0 linux/amd64
- Pre-existing uncommitted TUI work present since Process 00, green, untouched.

## What already exists (reuse, do not duplicate)

`internal/model/goal.go` carries a substantial Goal model, and it is good:

| Capability | Where | Verdict |
|---|---|---|
| `GoalContract` with revision, scope, constraints, do-not-do, success criteria | `model/goal.go` | **REUSE** — extend, do not replace |
| Typed `Constraint` with `IsHard` and source | `model/goal.go` | **REUSE** |
| `Assumption` with risk and reversibility | `model/goal.go` | **REUSE** |
| `UnresolvedDecision` for clarification | `model/goal.go` | **REUSE** |
| `EvaluateUnderstanding` → READY / NEEDS_INPUT | `model/goal.go` | **REUSE** |
| `ComputeGoalDiff` with re-evaluation trigger | `model/goal.go` | **REUSE** |
| `CanModifyGoal` — agents cannot weaken hard constraints | `model/goal.go` | **REUSE** — already enforces a central invariant |
| CAS-safe revision persistence | `store/goal.go` `SaveGoalContract` | **REUSE** |
| `goal_contracts` / `goal_active` tables (schema v73) | `store/migrations.go` | **EXTEND** |
| Advisory contract, self-check, gate | `internal/constitution` (Process 00) | **REUSE** |
| Project identity and scope | `internal/projectid` (Process 02) | **REUSE** |
| Harness governance states | `internal/harness` (Process 00) | **REUSE** |

## Genuine gaps against the pack specification

Seventeen conceptual fields from `87_GOAL_CONTRACT_SPEC.md` have no
representation. Two of them are P0-class.

### P0-03-01 — The user's own words are never preserved

There is no `OriginalRequest` field, and no column for one. The Goal stores a
`DesiredOutcome` — MARSHAL's *interpretation* — with nothing to check it
against.

Article III requires that the original request stay linked so every material
revision can be re-checked against the accepted intent. Without it, drift is
undetectable by construction: after a few revisions there is no artefact
recording what the user actually asked for, only successive interpretations of
interpretations.

### P0-03-02 — Goals are not bound to a project

`GoalContract` has `SessionID` but no `ProjectID`, and neither does the schema.
Process 02 established project identity precisely so state could be
project-bound; goals currently sit outside that. A goal cannot be checked
against the project it belongs to, and cross-project isolation does not reach
goal state.

### P1-03-03 — Risk is a single value; the pack requires ten dimensions

`GoalContract.Risk` is one `model.Risk` (R0–R3). `88_REQUEST_ASSESSMENT_MATRIX`
requires complexity, ambiguity, blast radius, privilege, reversibility,
external effects, data sensitivity, dependency depth, verification difficulty
and operational criticality assessed **independently**, explicitly forbidding
collapse into a single score.

Collapsing them is not a cosmetic loss. A large but wholly reversible
refactor and a one-line change to production credentials can land on the same
R-value while warranting opposite treatment.

### P1-03-04 — No confirmation state

Nothing records whether a goal was approved, is awaiting confirmation, was
delegated under ULTRA, or was cancelled. Process 04 has no canonical signal to
gate on, so "no planning without an approved goal" cannot be enforced.

### P1-03-05 — No provenance or revision reason

Revisions are versioned but carry no reason and no provenance, so a goal's
history shows *what* changed without *why* or *on whose authority*.

### P2-03-06 — No constitution version binding on the goal

Process 00 binds sessions to a constitution version; goals are not bound, so a
goal cannot record which rules it was formed under.

## Entrypoint map (task 04)

```
CLI          internal/cli/cli.go        → no goal command exists
TUI          internal/tui/commands.go   → /goal referenced in the status line
Store        store.SaveGoalContract     → CAS-safe, the canonical writer
Model        model.ValidateGoal         → the only validation gate
Constitution constitution.Evaluate      → gates material decisions (Process 00)
```

The status line renders `no goal set · /goal <outcome>`, so the TUI surface
anticipates a goal command. Formation, assessment and confirmation have no
implementation.

## Design conclusion

The existing Goal model is sound and stays. Process 03 adds what is missing:

1. **Request assessment** — ten independent dimensions, deterministic, with
   complexity and risk genuinely separate.
2. **Goal formation** — preserving the original request verbatim, binding to
   project and constitution, recording provenance and confirmation state.
3. **Advisory boundary** — Control Intelligence may propose an interpretation;
   MARSHAL forms the canonical Goal, and hard constraints survive regardless of
   what the model returns.

No new persistence is added where the existing tables suffice; a migration is
warranted only for the fields that genuinely cannot be represented.
