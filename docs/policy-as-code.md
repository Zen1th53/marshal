# Policy-as-Code (T48)

T48 is the canonical policy lifecycle and runtime policy boundary. Policy
content is parsed and validated by `internal/policy`; durable versions and
lifecycle state are owned by `internal/store`.

## Lifecycle

Policy versions start in `loaded` and may move only through:

```text
loaded → validated → active → superseded
```

Illegal and unknown transitions fail closed. Transitions use expected-state,
generation, and digest checks. Schema v7 also enforces at most one active
policy version in SQLite. Runtime selection treats a missing or ambiguous
active policy as unavailable and denies protected work.

## Authority boundaries

A04 management authorization answers who may validate, activate, or supersede
a policy. A06 runtime evaluation answers whether an active policy allows one
specific operation. A runtime `ALLOW` does not grant policy-management
authority. Policy content, provider/model labels, and historical evidence
cannot self-authorize management operations.

Bindings contain `PolicyVersion`, `PolicyDigest`, and `Generation`.
`PolicyBinding.FreshAgainst` compares all three exactly. Lifecycle state is a
mutation precondition; it is not silently added to the policy-content digest.

## Runtime behavior

When `RuntimePolicy` is configured, the runtime loads the configured canonical
policy ID/version, requires state `active`, evaluates before the side effect,
validates the decision binding, and rejects unsupported requirements. Missing,
corrupt, stale, or invalid policy infrastructure fails closed. An unconfigured
runtime retains the existing legacy behavior and does not claim T48 runtime
protection.

## Events, evidence, and metrics

Policy mutation events are durable and committed with the lifecycle mutation.
T06 evidence is historical evidence, not current authority. Metrics are a
detached, in-process, bounded projection; they are not persisted and never
participate in authorization. Policy persistence, load, transition, and
configured runtime-gate operations use closed metric vocabularies without
policy IDs, subjects, tasks, sessions, resources, providers, or raw errors.

## Policy test operator workflow (T49)

Run a declarative JSON suite before activating a policy:

```bash
marshal policy test policy-suite.json
marshal --json policy test policy-suite.json
```

The command parses one bounded JSON document (maximum 1 MiB), rejects unknown
fields and duplicate cases, evaluates the exact policy binding in the suite,
and does not create or mutate a durable policy-test run. `PASS` exits zero;
`FAIL`, evaluator `ERROR`, malformed input, and unavailable input exit
non-zero. JSON output includes the exact `policy_digest` and per-case typed
status/reason/diff. A failed example is therefore actionable without exposing
fixture text or evaluator output:

```text
status=FAIL policy_digest=sha256:<digest>
case-deny=FAIL diff=expected effect deny, got allow reason=EXPECTATION_MISMATCH
```

The durable `store.RunPolicyTest` path is separate: it uses A04-authorized
state transitions, a SQLite execution claim, owner-bound terminal CAS, and
schema v11 outcome reconstruction. A retry or restart re-reads canonical
state; it does not infer success from a lost response. The read-only CI
command does not self-authorize and does not activate policies.

When an `evidence.MetricsRecorder` is attached through the existing store
observability hook, T49 exposes the bounded `policy_test` operation with
success/denied/error/invalid counts, total monotonic duration, and a local
active-claim gauge. Reasons and labels use closed vocabularies only; policy,
run, task, case, subject, provider, digest, fixture, and raw error values are
not metric dimensions. Metrics are an in-process projection and cannot grant
authority or change a result. T49 case events remain the durable correlation
surface and contain IDs/digests/reason codes rather than raw fixtures.

## Concurrency and recovery

SQLite WAL, conditional updates, the schema v7 active-policy uniqueness index,
the schema v8 policy-test result projection, schema v9 lifecycle state, schema
v10 execution claims, schema v11 durable outcomes, and bounded busy/locked retries
provide the durable concurrency boundary.
Memory and process-local locks are not canonical state. Exact retries and
restart recovery re-read durable policy state; stale transitions return typed
conflicts. No distributed consensus or multi-host guarantee is provided.

## Operator limitations

The repository does not provide a T43 global event stream, T44 dashboard,
Prometheus/Grafana service, or Gemini review result. Full integration tests
that require TCP6, Unix sockets, or Bubblewrap networking may be unavailable
in restricted execution environments; those are environment limitations, not
policy success signals.

The in-process metrics snapshot is not a Prometheus endpoint and is local to
the process that owns the recorder; an active-claim gauge may be stale after a
crash. SQLite coordination is durable for the local database, not a
distributed multi-host consensus mechanism. Policy-test performance is bounded
by the configured suite limit of 1,024 cases and the 1 MiB CI file limit.
