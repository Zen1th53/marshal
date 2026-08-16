# Tool Call Risk Engine

T24 classifies structured tool metadata before MARSHAL runtime execution.
The runtime path is:

```text
app.Runtime.Run
  → risk.Engine.Assess
  → SQLite risk_assessments (state CAS)
  → existing gate/capability/policy boundaries
  → adapter/process execution
```

Adapters provide `tool`, `action`, `resource`, and structured factors such as
`external_write`, `destructive`, `secret_use`, `network`, `deploy`, and
`privilege_escalation`. Raw command text is not the risk source of truth.

Risk levels are `low`, `medium`, `high`, and `critical`. High and critical
assessments emit bounded requirements and never authorize an operation. An
existing authority must still approve any privileged action; if that authority
is unavailable, the request fails closed.

The durable assessment state is explicit:

```text
requested → classified → requirements_emitted
```

SQLite state transitions use conditional updates. Duplicate requests reconcile
from the canonical row, including across multiple Store instances. A stale
transition is rejected. The assessment table stores digests, normalized factors,
requirements, level, score, state, and timestamp—not command output or secret
values.

Structured events include `risk.assessment.created`, `risk.level.high`,
`risk.level.critical`, and `risk.override.denied`. Events are metadata-only and
are not authorization authority. Retries use deterministic event idempotency
keys.

The optional `risk` metrics operation records bounded success/invalid/denied/
conflict/error/cancelled counts and aggregate duration. Labels do not include
task IDs, resources, prompts, descriptors, policy text, or raw errors. Metrics
are an in-process projection and cannot alter a risk decision.

CLI/protocol/provider callers reach this path through `app.Runtime.Run`; no
provider-specific risk semantics are defined. Direct API callers may use
`Runtime.AssessTool` with a structured `risk.AssessmentRequest`.

Malformed, control-character, secret-marker, and traversal-like descriptors are
rejected with `RISK_DESCRIPTOR_INVALID`. Unknown mutating actions with an
external-write factor receive at least `high`; a lower caller-claimed level is
rejected with `RISK_DOWNGRADE_FORBIDDEN`.
