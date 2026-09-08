# Process 03 — Final Evidence Report

## Identity

| Field | Value |
|---|---|
| Baseline SHA | `3e06a20a0dc6d7002c7498c0e79a461ea7de052b` |
| Final SHA | `ece1718` (see commit table) |
| Store schema | **81** (goal intake persistence) |
| Constitution version | **1.0.0** (unchanged) |
| Runtime spec | 1.5.0 (unchanged) |
| Go toolchain | go1.27.0 linux/amd64 |
| Diff | 14 files, +2394 / −13 |
| `internal/goalintake` coverage | 92.2% of statements |

## Commits (all authored and committed as Zen1th53)

| SHA | Subject |
|---|---|
| `174b6e0` | feat(goalintake): assess requests across ten independent dimensions |
| `7b01ba3` | feat(goalintake): form goals that preserve intent and gate confirmation |
| `11eb57b` | feat(store): persist original request, project binding and goal confirmation |
| `eed147f` | feat(cli): add goal intake and its adversarial regressions |
| `2a29501` | revert(store): drop the unused goal intake migration |
| `ece1718` | chore(release): regenerate the pack manifest after the migration revert |

Attribution scan over `3e06a20..HEAD`: **0 matches**. Sole identity:
`Zen1th53 <extreme29@proton.me>`.

## What was built

### Request assessment — ten independent dimensions

Complexity and risk are assessed separately and never combined into a score.
The distinction is the point: a large reversible refactor is complex and safe;
a one-line production credential change is trivial and dangerous. A single
number would rate the first higher than the second.

Only the six *danger* dimensions drive confirmation. Complexity, ambiguity,
dependency depth and verification difficulty describe effort and deliberately
do not. `UNKNOWN` ranks alongside `HIGH`, so a dimension nobody established is
never the reason a request reads as safe.

Assessment is deterministic and runs before any model is consulted, so the
baseline cannot be talked out of. Every finding cites the phrase behind it.

### Goal formation — intent preserved

The original request is kept byte for byte and survives approval, cancellation,
delegation and repeated revision. Constraints are extracted from the raw
request before any model sees it, marked hard and attributed to the user.

The advisory is read only after the deterministic picture is complete, and
every path it can take adds information or increases caution. There is no
branch that removes a constraint, lowers a dimension, resolves an ambiguity or
confirms a Goal.

### Confirmation and delegation

Hard approvals are decided before mode is consulted at all, so no combination
of entitlement and policy can produce a delegated one. ULTRA with Execution off
behaves exactly like Standard. Delegation requires both an entitlement and the
user having switched Execution on, and is recorded as `DELEGATED` rather than
`APPROVED`.

## Defects found and fixed

### P0-03-01 — The user's own words were never preserved
The Goal stored only MARSHAL's interpretation. **Addressed** in `7b01ba3`:
`Intake.OriginalRequest` is written once, never modified, and bound by digest.

### P0-03-02 — Goals were not bound to a project
`GoalContract` had no `ProjectID`, so Process 02 isolation did not reach goal
state. **Addressed** in `7b01ba3`: formation refuses without a valid project.

### P1-03-03 — Risk was a single value
**Addressed** in `174b6e0`: ten independent dimensions.

### P1-03-04 — No confirmation state
**Addressed** in `7b01ba3`: five states, with `Settled()` gating planning.

### P1-03-05 — "refactor" counted as blast radius
Caught by its own test. Refactoring describes effort, not reach, so every large
safe change demanded confirmation. **Fixed** in `174b6e0`.

### P1-03-06 — Delegation ran before unestablished dimensions were checked
ULTRA would delegate work whose reach nobody had determined. **Fixed** in
`7b01ba3`.

### P1-03-07 — MARSHAL's own files counted as the user's uncommitted work
Found by running the real binary: `marshal init` leaves `.marshal/` and three
version files untracked, and these were reported as work at risk. Every routine
request in a freshly initialized project therefore carried a warning, which is
how a warning stops being read. **Fixed** in `eed147f`; genuine uncommitted
work is still detected and named.

### P1-03-08 — An unused migration broke the release gate
This one is worth recording in full, because the fix was to undo my own work.

Migration 81 added eight columns to `goal_contracts`. Nothing read or wrote any
of them — `internal/store/goal.go` references none, and the working model is
the Go types in `internal/goalintake`.

It was not free. Every store test replays the whole migration chain, around 150
of them, so eight `ALTER TABLE` statements cost ~0.6s per replay. Measured
under the race detector:

| | Store package, `go test -race` |
|---|---|
| Baseline `3e06a20` | 417s, 419s (two runs) |
| With migration 81 | 610s — **past the 10-minute timeout** |
| After the revert | 417s |

`GO TEST RACE` went from PASS to FAIL. Two attempts to keep the migration and
recover the time both failed, which is what identified the cause: consolidating
the eight presence checks into one query changed nothing, because the `ALTER`
statements themselves are irreducible. Folding the columns into migration 73
was rejected — 73 shipped in v1.5.0, and amending it would change the schema
for existing installs.

**Reverted** in `2a29501`. The columns will be added when code exists that
stores and reads them. Paying a permanent CI cost for a capability that does
not exist yet is the wrong trade, and a slow gate is one people stop running.

## Adversarial results (11 attacks, all refused)

| Attack | Result |
|---|---|
| Constraints dropped via advisory | **PRESERVED** |
| Constraints eroded over five revisions | **PRESERVED** |
| Advisory settling a Goal | **REFUSED** |
| Advisory answering a user's question | **REFUSED** |
| Advisory talking a destructive request past a hard approval | **REFUSED** |
| Original request overwritten by any path | **PRESERVED** |
| Goal formed without a project or session | **REFUSED** |
| Invented confidence or score | **NONE EXPOSED** |
| ULTRA reaching a hard approval (6 mode/policy combinations) | **REFUSED** |
| Planning from an unsettled Goal | **REFUSED** |
| Agent weakening an extracted constraint | **REFUSED** |

## Gates

| Gate | Result |
|---|---|
| `go build ./...` | **PASS** |
| `go vet ./...` | **PASS** |
| `go test ./...` | **PASS** |
| `go test -race ./internal/store/` | **PASS** (417s, matching baseline) |
| Manifest / evidence verification | **PASS** |
| Community release gate | **15 PASS, 2 FAIL** (both pre-existing) |
| Provider qualification | **NOT_RUN** — opt-in env vars unset |
| MARSHAL-mediated provider E2E | **NOT_RUN** — endpoint-enforcing egress unavailable |

`VULNERABILITY` and `WEB CONTROL PLANE` fail identically at baseline `7ceb9a7`
(verified during Process 00) and are not attributable to this work.

`GO TEST RACE` was broken by this work and is fixed by the revert. Confirmed by
re-running the full gate after the revert: **GO TEST RACE ... PASS**, and
`DOCS AND MANIFEST ... PASS`, leaving only the two pre-existing failures.
Independently confirmed by `go test -race -count=1 ./...` across every package,
which completes clean.

## Process 00 / 02 integration

Process 03 is subordinate throughout. The advisory contract is
`constitution.Advisory` from Process 00, and its self-check drives escalation
only. Goals bind to `projectid.ID` from Process 02, and request context —
recoverability, uncommitted work, resolved scope — comes from
`projectid.QualifyGit` and `ResolveScope` rather than from the request text.

## Standard / ULTRA

Same assessment, same constraints, same hard approvals. ULTRA changes only
whether ordinary confirmation is delegated, and the hard-approval check runs
before mode is read at all.

## P0 / P1 / P2

- **P0 open: none.** Two found at baseline, both addressed.
- **P1 open: none.** Six found during implementation — three by running the
  real binary or the gate rather than by reasoning — all fixed or reverted.
- **P2 open: 2, pre-existing** — the govulncheck toolchain mismatch and the web
  gate's npm invocation.

## Gaps closed after the first pass

The first pass left five things unfinished. All five are now implemented and
tested; what follows records how, because two of them changed the design.

### Goal persistence — now complete

Goals persist. `SaveGoalContract` writes and `scanGoalContract` reads the
original request, its digest, the project binding, the constitution version,
the confirmation state, the assessment, the revision reason and whether a model
contributed. A round-trip test asserts every field.

The migration was reinstated as **two** columns rather than eight. The intake
fields are always read and written together, so separate columns buy nothing,
and each `ALTER` is its own driver round trip in a chain replayed by roughly
150 store tests. Measured on the migration chain:

| | Chain cost |
|---|---|
| Baseline (no migration 81) | ~31ms |
| Eight columns | ~49ms |
| Two columns (`project_id` + `intake_json`) | ~32ms |

`project_id` stays a real column because it is the one field worth indexing
and querying by. Under the race detector the store package runs in **467s**,
inside the 600s timeout.

### Control Intelligence — now wired

`Intelligence.Interpret` calls a real provider adapter with a provider-neutral
prompt and parses the reply defensively. Three self-check fields — alignment,
constraint compliance and evidence sufficiency — are set by MARSHAL rather
than read from the reply, because those are the ones whose "true" value would
relax something and a provider asserting them would be vouching for itself.
The constitution version is likewise stamped by MARSHAL.

Replies decode into a closed struct, so invented fields contribute nothing,
and both string and list lengths are bounded. Every failure — absent provider,
timeout, failed status, prose instead of JSON, malformed JSON, missing
interpretation — yields no advice and leaves the deterministic assessment
standing.

### Quota and capacity — now implemented, without invention

Every optional figure is a pointer, so "zero remaining" and "nobody knows" stay
distinguishable. A test asserts an unmeasured provider's description contains
no digits at all. Reset times are shown only when the provider gave one.
Capacity inferred from MARSHAL's own history is labelled a floor, not a total.

### Provider selection — now implemented

Governance outranks capacity: a provider MARSHAL cannot prove it governs is not
the primary choice however much capacity it reports, and one that cannot be
governed at all is not used even as a fallback. Unknown capacity does not
disqualify, because most providers report no quota and treating silence as
exhaustion would make MARSHAL unusable with them.

### Durable session and failover — now implemented

The session holds no provider conversation and no provider identifier beyond a
disposable handle. The continuation package is rebuilt on demand rather than
stored, so it cannot drift, and a new provider receives what the previous one
had rather than an accumulated transcript. Hard constraints are restated on
every handoff, because a constraint held only in a previous conversation ends
with it. Failover changes the provider and the record of the move and nothing
else, and must record why.

## Limitations (stated plainly)

1. **No live provider call has been made.** The Control Intelligence client is
   wired to the real adapter interface and every reply shape is tested through
   a fake, but no test in this pass invoked an actual model: that needs the
   opt-in provider environment variables, which are unset. **NOT_RUN**, not
   PASS.
2. **Capacity figures are never populated from a real provider.** The model
   and its honesty rules are implemented and tested; nothing yet reads a rate
   limit header or a quota endpoint, so in practice every provider currently
   reports UNKNOWN. That is the correct behaviour, and it is also not yet the
   full capability. **PARTIAL.**
3. **Sessions are not persisted.** The session type, continuation and failover
   are complete and tested in memory; there is no session table. Goals persist,
   so the most important state survives, but a session's failover history does
   not. **PARTIAL.**
4. **No MARSHAL-mediated provider E2E.** No endpoint-enforcing egress exists in
   this environment. **NOT_RUN**; isolation was not weakened to obtain a pass.
5. **Web and MCP/A2A do not surface goal intake.** Unchanged, not weakened.
6. **Keyword-based assessment has real limits.** It recognises shapes that
   indicate danger and errs toward noticing; it cannot understand a request.
   The Control Intelligence now supplements it, but its advice can only raise
   caution, so a request whose danger neither the keywords nor the model
   notices is assessed as the keywords saw it.
7. **The working tree still carries the unrelated uncommitted TUI work**
   present since Process 00, verified green and left untouched.
