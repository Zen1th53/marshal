# MARSHAL TERRA Completion Execution Design

## Context

MARSHAL TERRA v3 defines 55 epics with ten atomic tasks per epic, a hard dependency DAG, execution waves, security invariants, release gates, and a Codex/Gemini pair protocol.

The recovered Git history and the uploaded source snapshot agree on the current canonical implementation baseline:

- baseline commit: `c1442e6a827fa3e14aa88c295ceaa8893551a151`;
- `marshal-main.zip` matches the baseline Git tree for all 518 tracked files;
- completed epics on the baseline: T06 Evidence Graph, T48 Policy-as-Code, T49 Policy Test Framework;
- remaining epics: 52;
- remaining Wave 0 prerequisites: T29 Dynamic Task DAG and T43 Structured Event Stream.

All new repository commits must use:

- author/committer: `Zen1th53 <extreme29@proton.me>`;
- no AI co-author, generated-by, or authorship trailers.

## Goal

Complete the remaining TERRA epics while preserving the authoritative dependency DAG, atomic-task barriers, zero-trust invariants, evidence-backed verification, recoverable Git history, and final reproducibility.

The final deliverables are:

1. a fully verified MARSHAL source tree;
2. a complete Git bundle containing the resulting history;
3. a source ZIP produced from the verified final tree;
4. a completion report mapping every TERRA epic and atomic task to exact commits and verification evidence.

## Non-goals

- Do not execute epics blindly in numeric order.
- Do not merge unrelated epic scopes into giant diffs.
- Do not weaken TERRA security invariants to increase throughput.
- Do not treat unavailable external verification as a pass.
- Do not rely on process-local memory as the only record of completed work.
- Do not rewrite existing T06/T48/T49 history merely to normalize style.

## Authoritative Inputs

Priority order:

1. `MARSHAL-TERRA-v3/MASTER/*` and the selected epic/atomic specification;
2. canonical repository state and executable tests;
3. existing project protocols, registries, and integration contracts;
4. historical conversation/context only as explanatory context, never as a replacement for repository/spec truth.

If implementation reality conflicts with a security invariant, stop that lane and report the blocker. Never weaken the invariant to make progress.

## Baseline and Branch Model

The completion line starts from:

`c1442e6a827fa3e14aa88c295ceaa8893551a151`

A dedicated integration branch is maintained for this sandbox execution. Each epic is implemented on an isolated branch/worktree derived from the latest dependency-valid integration state.

Each atomic task produces at least one coherent commit with exact test evidence. An epic is integrated only after A01-A10 and its Definition of Done pass.

No force-push workflow is required for local completion. Final history is exported as a Git bundle.

## Execution Strategy

### 1. Finish Wave 0 first

The next required epics are:

- T29 — Dynamic Task DAG
- T43 — Structured Event Stream

They are dependency-valid against the current baseline and may be developed in independent isolated lanes because neither depends on the other.

Wave 1 does not begin until both are complete and the Wave 0 gate passes.

### 2. Dependency-aware parallel lanes

Within an epic, A01 through A10 remain serial unless the authoritative atomic specification explicitly permits independent internal sub-work.

Across epics, independent dependency-valid epics may execute in parallel lanes. Parallel work must never share an uncommitted mutable worktree.

A scheduler for this completion effort selects only epics whose declared dependencies have passed their acceptance gates.

### 3. Atomic task lifecycle

For every A01-A10:

1. read the exact atomic specification and referenced master registries;
2. reconcile prior atomic reports with the current tree;
3. create an isolated branch/worktree;
4. write the first meaningful failing test when production behavior changes;
5. implement the minimum complete behavior;
6. run focused, adversarial, race, integrity, secret-hygiene, and release-pack checks required by the spec;
7. perform an independent review lane where available;
8. commit using Zen1th53 identity;
9. record exact SHA and evidence;
10. continue only if no unresolved HIGH/CRITICAL findings remain.

### 4. Epic lifecycle

After A10:

1. run the epic Definition of Done;
2. run the release-pack verifier and deterministic regeneration check;
3. run full applicable repository verification;
4. integrate the epic onto the completion integration branch;
5. verify ancestry and resulting repository state;
6. record the epic completion checkpoint.

### 5. Wave lifecycle

At every wave boundary run, at minimum:

- `go test ./...`;
- `go vet ./...`;
- `go test -race ./...`;
- `git diff --check`;
- available adversarial/conformance suites;
- secret/pre-release hygiene checks;
- release-pack verification.

Environment-restricted failures may be classified only from fresh evidence and never converted into a pass.

## Security Model

The completion workflow preserves the TERRA zero-trust model:

- provider/model labels never grant trust;
- caller claims never self-authorize privileged operations;
- policy/evidence/history are distinct from current authority;
- fail-closed behavior is mandatory on ambiguous or unavailable authority;
- secrets must not enter logs, evidence payloads, metrics labels, commits, or release artifacts;
- durable state transitions use database/state-machine invariants rather than process-local mutexes alone;
- stale authorization and replay are rejected;
- evidence of success must bind to the exact canonical operation and state;
- verification may not certify the verifier's own implementation as independent review.

## Pair / Independent Review Handling

The TERRA pair protocol remains authoritative.

If Gemini or another required independent lane is unavailable because of environment, authentication, or quota limitations:

- record `BLOCKED / NOT EXECUTED` with the exact reason;
- never fabricate a PASS;
- use additional read-only/adversarial internal review where permitted;
- do not silently waive an epic-level hard gate.

Any final waiver of an authoritative hard gate requires explicit owner approval and must be recorded as a waiver, not as verification success.

## Throughput Model

The main throughput optimization is epic-level parallelism, not weakening atomic serial order.

Recommended behavior:

- keep 2-4 dependency-independent epic lanes active when repository/test capacity allows;
- keep A01-A10 serial within each epic;
- pause only the affected lane when a blocker is local to that epic;
- stop downstream dependent lanes when a dependency gate is not satisfied;
- periodically integrate completed epics to reduce long-lived divergence.

## Recovery and Checkpointing

Every completed atomic task is durable in Git. Every completed epic is durable on the integration line.

The workflow must be restartable from repository state alone by reading:

- current integration HEAD;
- completed epic/atomic commits;
- TERRA DAG and wave definitions;
- generated completion ledger/report.

No hidden conversation state is required to resume correctly.

## Completion Ledger

Maintain a machine-readable or structured repository-local ledger/report containing for each epic and atomic task:

- status;
- dependency validation;
- branch;
- commit SHA(s);
- test commands/results;
- security findings;
- independent review status;
- release-pack status;
- known limitations.

The ledger is evidence/indexing metadata, not an authority source.

## Final Verification

TERRA completion is declared only when:

- all 55 epics are DONE according to their own Definition of Done;
- no unresolved HIGH/CRITICAL findings remain;
- all dependency/wave gates are satisfied;
- required registries/contracts are consistent;
- release-pack verification passes;
- final repository verification is evidence-backed;
- final Git history is internally consistent;
- final source ZIP is generated from the verified tree;
- final Git bundle verifies successfully.

## Final Artifact Set

Produce:

- `MARSHAL-TERRA-complete.zip`
- `marshal-terra-complete.bundle`
- `MARSHAL-TERRA-COMPLETION-REPORT.md`
- SHA-256 checksums for each deliverable.

The final ZIP contains source/runtime assets only as allowed by repository packaging rules; it must not contain `.git`, credentials, temporary databases, caches, test artifacts, or local environment secrets.

## Decision

Use `c1442e6a827fa3e14aa88c295ceaa8893551a151` as the canonical completion baseline. Complete T29 and T43 next, then advance strictly through the dependency-valid TERRA waves. Preserve atomic A01-A10 barriers, parallelize only independent epics, checkpoint every task in Git under Zen1th53 identity, and export the final verified implementation as both ZIP and Git bundle.
