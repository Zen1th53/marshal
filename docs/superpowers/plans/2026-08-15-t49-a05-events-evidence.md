# T49.A05 Events and Evidence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Record each security-relevant T49 lifecycle outcome as bounded, deterministic audit evidence in the existing `audit_events` graph, atomically with the authorized A03 transition.

**Architecture:** Reuse `model.Event` and `Store.AppendEvent` as the canonical T06 evidence/audit boundary, and mirror T48's deterministic event-ID and scalar-data pattern without creating a second graph or schema. The A04 authorized T49 transition will load canonical run facts, validate authority, perform the A03 CAS and append one deterministic T49 event in the same SQLite transaction; denial remains a non-mutating, separately typed event when applicable.

**Tech Stack:** Go, `database/sql`, SQLite, existing `internal/model`, `internal/store`, `internal/policy`, and `internal/policytest` packages.

## Global Constraints

- Do not implement A06 runner/provider execution or automatic PASS/FAIL derivation.
- Evidence and events are historical projections, never authorization.
- Reuse T06 `model.Event`/`Store.AppendEvent`; do not create another event graph.
- Preserve A01-A04 contracts, schema v9, A04 authorization, and A03 CAS semantics.
- Store only bounded scalar IDs/digests/states/actions/reason codes; never raw fixtures or backend errors.
- Use deterministic event identities so exact retries cannot create contradictory duplicates.
- Every production change is preceded by a failing focused test and followed by focused, race, regression, and release verification.

## Task 1: Add the failing success-evidence contract test

**Files:**
- Modify: `internal/store/policy_test_authorization_a04_test.go`
- Test: `internal/store/policy_test_events_a05_test.go`

**Interfaces:**
- Consumes: `Store.TransitionPolicyTestRunStateAuthorized`, `policytest.AuthorizationRequest`, `policytest.AuthorizationDecision`.
- Produces: A failing assertion that a committed `loaded -> validated` transition has one `policytest.started` event bound to the exact run and policy facts.

- [ ] Write `TestPolicyTestRunAuthorizedTransitionCommitsBoundEvent` using the existing `a03TestRun`, `a04Request`, and `a04Allowed` helpers. Assert the run becomes `validated`, exactly one event of type `policytest.started` exists, and its scalar data includes run ID, policy ID/version/digest/generation, test-file digest, previous/target state, action, subject/session/task/change, and a stable success reason.
- [ ] Run `go test ./internal/store -run '^TestPolicyTestRunAuthorizedTransitionCommitsBoundEvent$' -count=1` and confirm RED because A05 does not yet emit the event.

## Task 2: Define the closed T49 event vocabulary and bounded payload

**Files:**
- Create: `internal/policytest/events.go`
- Test: `internal/policytest/events_test.go`

**Interfaces:**
- Consumes: `RunState`, `Action`, `policy.PolicyBinding`, and stable policy authorization errors.
- Produces: Closed `EventType` values (`policytest.started`, `policytest.case.passed`, `policytest.case.failed`, `policytest.finished`) and validation for bounded scalar event facts.

- [ ] Add `EventType.Valid` with only the four authoritative A05 names; reject unknown event types.
- [ ] Add a typed `EventFact` containing only bounded scalar correlation fields: `RunID`, `PolicyID`, `PolicyVersion`, `PolicyDigest`, `Generation`, `TestFileDigest`, `PreviousState`, `TargetState`, `Action`, identity fields, `Result`, and `ReasonCode`.
- [ ] Add deterministic validation that rejects empty/oversized IDs, invalid digests/states/actions, unknown result/reason codes, and arbitrary metadata.
- [ ] Test unknown event/reason values, bounds, defensive value semantics, and secret-marker exclusion from validation errors.
- [ ] Run `go test ./internal/policytest -run 'Event|Reason|Bound' -count=1` and confirm GREEN.

## Task 3: Integrate deterministic T49 events into the authorized transition transaction

**Files:**
- Create: `internal/store/policy_test_events.go`
- Modify: `internal/store/policy_test_store.go`
- Test: `internal/store/policy_test_events_a05_test.go`

**Interfaces:**
- Consumes: T06 `model.Event` and `Store.AppendEvent`, A04 authorized transition request, A03 internal CAS transaction.
- Produces: `appendPolicyTestEvent(ctx, tx, EventFact)` and atomic authorized lifecycle mutation with one deterministic event.

- [ ] Implement deterministic event-ID derivation from event type plus canonical JSON payload using SHA-256, matching T48's idempotent event pattern.
- [ ] Implement bounded payload conversion to `model.Event.Data`; never include raw fixtures, policy bodies, authorizer decisions, or backend errors.
- [ ] Refactor the internal T49 CAS helper to accept the already-open transaction and append the required event before commit. Preserve expected-state CAS and canonical reload.
- [ ] Map lifecycle edges without deriving test results: `loaded -> validated` emits `policytest.started`; `executed -> passed` emits `policytest.finished` with `passed`; `executed -> failed` emits `policytest.finished` with `failed`; intermediate `validated -> executed` emits no terminal result inference.
- [ ] Keep A04 deny/error/stale paths non-mutating; if a denial fact is required by the event contract, emit only a bounded `policytest.started`/`finished`-free denial event in its own transaction and never a success event.
- [ ] Run the Task 1 test and affected store tests until GREEN.

## Task 4: Add rollback, replay, authority-separation, restart, and secret tests

**Files:**
- Modify: `internal/store/policy_test_events_a05_test.go`

**Interfaces:**
- Consumes: authorized transition API and `ListEvents`.
- Produces: regression proof for A05 atomicity and historical-evidence semantics.

- [ ] Test dropping/failing `audit_events` causes the run transition to roll back to `loaded` with no success event.
- [ ] Test deny, authorizer error, malformed/expired decision, illegal edge, stale CAS, terminal escape, and cancellation produce no false-success event.
- [ ] Test exact retry after a committed transition is reconcilable and leaves one semantic success event after reopen.
- [ ] Test prior evidence cannot authorize a subsequent transition without A04 authority; expected ALLOW/PASS and a target T48 policy remain non-authoritative.
- [ ] Test event fields preserve exact binding and reject wrong-run/policy/digest/generation/test-file associations; verify no raw fixture or authorizer error is persisted.
- [ ] Test two stores produce one winner and no loser success evidence.
- [ ] Run focused A05 tests and `PRAGMA integrity_check`/foreign-key checks.

## Task 5: Regression, release verification, and checkpoint

**Files:**
- Modify: `distribution/PACK-MANIFEST.json` if legitimate tracked files require regeneration.

**Interfaces:**
- Consumes: all A01-A05 package contracts and T06/T48 regression suites.
- Produces: pushed `feat/t49-policy-tests-a05-codex` with exact local/remote SHA match.

- [ ] Run focused policytest/store/evidence/policy/app tests.
- [ ] Run repeated A05 security tests (`-count=25`) and focused race tests (`-race`, `-count=5`).
- [ ] Run `go vet ./...`, full `go test ./...`, full `go test -race ./...`, and `git diff --check`; classify only proven TCP6/Unix-socket/Bubblewrap restrictions as environmental.
- [ ] Run release verifier, regenerate only when required, and verify deterministic regeneration.
- [ ] Review diff for secrets, raw fixture/error persistence, A06/A43 scope creep, and raw A03 bypasses.
- [ ] Commit `feat(T49.A05): integrate policy test events and evidence`, push the A05 branch, and verify local/remote SHA equality.
