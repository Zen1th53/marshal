# Security Gate Engine

The gate engine evaluates one of MARSHAL's five gate points: pre-execution,
pre-commit, pre-push, pre-merge and pre-release. It snapshots a validated
policy digest, runs the configured required checks, rejects stale evidence and
returns a structured decision. A missing or unknown required check is a
fail-closed error; it is never treated as a warning or a pass.

## Runtime boundary

When `app.Options.GateEngine` is configured, `Runtime.Run` evaluates the
pre-execution gate before task claim, provider probing or adapter execution.
An allowed decision is persisted before its bounded audit projection. The
gate authorizes but does not execute a shell, provider, Git or network action.
The configured runtime gate is therefore an enforcement hook; an unconfigured
runtime does not claim gate enforcement.

Example success: a configured `secret-scan` check returns `PASS` with fresh
evidence, producing an `allowed` decision bound to the policy digest.

Example denial: a check returns `FAIL`, its evidence is expired, or the check
is absent from the registry. The decision is denied/blocked with a typed
reason such as `GATE_POLICY_DENY`, `GATE_STALE_EVIDENCE` or
`GATE_UNKNOWN_CHECK`.

## Persistence, events and recovery

Gate decisions are stored in SQLite (`gate_decisions`, schema v18) with an
explicit state machine: `requested → evaluating → allowed|denied|blocked →
consumed|invalidated`. State changes use a conditional update, so competing
workers cannot both advance the same state. Identical decision writes are
idempotent. If audit delivery fails after the database commit, the durable
decision remains authoritative and retry reconciles it.

Audit projections use the canonical `gate.allowed`, `gate.denied`,
`gate.blocked`, `gate.decision.consumed` and `gate.decision.invalidated` event
types. Resources are hashed in event references; raw evaluator errors,
prompts and secret material are not persisted.

## Diagnostics and limitations

`Engine.EvaluateObserved` records aggregate `gate` success/denial/invalid
counts and duration through the existing process-local metrics recorder. IDs,
resources, policy digests and raw errors are not metric dimensions. Metrics and
events are projections and cannot grant authority or change a decision.

The engine does not execute expensive checks itself; callers provide bounded
check functions whose results include evidence freshness. Deployment-specific
policy/configuration must register the required checks and configure the
runtime option before claiming enforcement.
