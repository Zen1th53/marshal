# Runtime Memory Lifecycle

MARSHAL wires bounded memory enhancement into the normal `Runtime.Run` path. It is an enhancement, not a second task authority or a second memory store: `memory_records_v2` remains canonical, while runtime traces and utility aggregates are rebuildable derived state.

## Task start

Before a provider receives its trusted context, MARSHAL:

1. renders the task context and observes the repository `HEAD`;
2. reads at most 128 canonical records in the current project and execution scope;
3. rejects tombstoned, rejected, superseded, conflicted, invalid-as-of, out-of-scope, and secret-bearing records;
4. ranks remaining records with the existing planner, reciprocal-rank fusion, finalizer, authority signals, and a bounded utility adjustment; and
5. sends only optional admitted records through the existing context budget manager and `CompileWithMemory`.

The context budget is 4096 estimated tokens with a 512-token reserve. Mandatory task context wins over memory. Recall, derived-index, trace, or context-compilation failures degrade to the original task context; they do not change a task into a false failure.

Project, task, agent, session, and branch scopes are checked again at the runtime boundary. Team and operator-private memories are not automatically injected. Retrieved content is explicitly delimited as untrusted data.

Fresh repository evidence wins. A commit-bound durable or verified record is excluded when its bound commit differs from current `HEAD`; MARSHAL does not mutate, supersede, or promote that record merely because a commit changed. Weak candidate episode/finding/decision records may be retained only as explicitly labelled **historical, untrusted** context, so a previous run can help diagnose a related task without becoming current repository truth.

## Task completion

After `FinishRun` commits, MARSHAL records an idempotent terminal event and best-effort memory work:

- admitted memory IDs receive one durable success/failure outcome per run;
- an episodic record captures status, commits, and evidence references; and
- the existing reflector writes an evidence-bound candidate decision or finding.

No raw provider output is copied into these records. The sole retained task text is an objective only when it passed the runtime byte sanitizer and memory firewall; otherwise capture continues without it. All writes pass the canonical memory firewall. Replayed completion handling uses the canonical run ID, so it cannot create unbounded episode/reflection records or amplify utility.

Memory capture or reflection failure never revises a task result that was already committed. Recovery can safely retry the independent episode, reflection, and outcome steps.

## Outcome utility and traces

`memory_runtime_outcomes` deduplicates `(run_id, memory_id)` and `memory_utility_scores` stores Laplace-smoothed success/failure counts. Utility contributes at most `±0.05` to the final score. It is applied only after scope, lifecycle, conflict, temporal, staleness, firewall, and authority checks, and cannot promote or bypass any of them.

`memory_runtime_traces` stores a bounded, content-free receipt for each execution: the run/task/project IDs, a SHA-256 query digest, observed `HEAD`, candidate IDs, admission/exclusion reasons, estimated tokens, and admitted IDs. It intentionally does not store memory bodies, task text, or provider output.

Operators can inspect the latest real receipt with:

```text
GET /api/v1/memory/retrieval/explain?task_id=TASK-...
```

The live endpoint returns no trace for a task with no recorded run and does not return the old development fixture when a runtime-backed store is present. Standard web authentication remains required.

## Observability and limits

The runtime receipt exposes candidate count, decisions, reasons, requested/admitted token totals, and the exact IDs that entered provider context. These fields support incident diagnosis and offline evaluation without high-cardinality metric labels or memory-content disclosure.

The current production path uses the canonical bounded candidate scan plus planner/fusion/finalization. Optional vector and graph projections remain disposable and may continue to serve explicit retrieval paths; their absence never blocks task execution.
