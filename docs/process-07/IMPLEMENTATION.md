# Process 07 implementation

Process 07 turns a bounded Process 06 outcome into durable, provenance-aware,
revocable knowledge. Verified experience may inform future decisions; it does
not become truth by repetition.

The implementation consists of:

- `internal/learning`: the epistemic memory model. Claims carry scope,
  provenance, evidence refs with source clusters, dependencies, contradiction
  refs, criticality, freshness and applicability bounds. Promotion is
  evidence-gated, revision keeps history, invalidation is dependency-targeted,
  and routing learning passes a governance veto it cannot relax;
- `internal/store`: schema v84 durable memory. Commits are append-only and
  digest-protected; items are mutable only through CAS on `(item_id, version)`;
  every read verifies the stored digest;
- `internal/app`: the canonical Process 07 service and the sole mutation
  boundary. It derives the Process 06 entry binding from the stored completion
  attestation, so a caller cannot supply the attestation digest, the evidence
  digest or the outcome;
- CLI, TUI, Web, MCP and A2A surfaces that read canonical durable state. Every
  Process 07 surface is read-only.

## Entry binding

A memory commit binds to one exact Process 06 state: project, goal revision,
plan version, run version, verification version, attestation digest, evidence
bundle digest, tree digest and environment digest. The service reads all of
these from canonical records rather than accepting them.

Failed, partial and blocked outcomes are ingested; failure is valuable
learning. A decision that is not one of those four, such as a cancellation, is
refused rather than flattened into `FAILED`. Without an attestation there is
nothing to bind to, so learning that depends on it is blocked.

## Promotion

Consensus does not promote. Prestige does not promote. Repetition does not
promote. Only evidence promotes, and evidence is counted by independent source
cluster, so one source echoed many times counts once.

Project-local is the default scope. Generalizing beyond the project that
produced a fact requires two independent clusters, independent corroboration
and explicit applicability bounds. A critical claim additionally requires a
verified state, two clusters and replayable evidence. A `VERIFIED` item cannot
come from a run that merely finished: the outcome must actually be
`VERIFIED_COMPLETE`. Secrets never enter memory, at any scope.

A candidate that fails a gate is recorded in `BlockedLearning` with its reason
rather than silently dropped or weakened, so the refusal is auditable.

## Revision, freshness and integrity

Revisions never mutate the prior version; it stays readable in
`memory_item_revisions`. Fresh deterministic evidence outranks stored knowledge
regardless of the prestige of the source that stored it. Retracting needs no
evidence; strengthening does.

Dependency invalidation is targeted and indexed, so upgrading one tool stales
only the knowledge that rested on that tool. A dependency reported at the
version an item already rests on is not a change. Invalidation is terminal: a
later dependency change does not downgrade an invalidated item back to stale.

CAS refuses a stale write, and a refused revision aborts its whole commit
rather than landing half of it, so an invalidation cannot be lost to a
concurrent promotion. Reads verify item and commit digests, so a claim whose
text, scope or evidence mapping was altered in storage is reported as tampered.

## Retrieval and re-injection

Retrieval is project-, scope-, freshness- and contradiction-aware, and always
bounded. Stale and contested items come back marked unusable rather than
filtered away, so absence never reads as irrelevance. Ranking is term overlap;
no confidence percentage is invented.

Re-injected context keeps canonical constraints binding and learned memory
advisory. Unresolved contradictions are injected explicitly. Stale memory is
never injected as fact.

## Retention, export and collection

Retention is explicit and by data class. Expiry drops raw payloads while
keeping the digest and provenance an active attestation rests on, so collection
never breaks the audit chain. Canonical claims are archived rather than
deleted. Secrets are purged whatever references them.

Exports are secret-safe, digest-protected and name what they redacted, so an
incomplete backup says so. A restore validates integrity and schema, and never
overwrites newer or invalidated canonical memory.

## Honesty about measurement

Unmeasured cost and latency stay nil and render as `unmeasured`, never as zero.
Routing aggregation includes failures, blocked runs and routes that were never
selected, and carries a selection-bias flag, so a provider handed only easy
work cannot appear universally strong. Observed harness capability is stored
separately from the documented claim. A verifier that misses mutations, reports
false negatives or is flaky may not stand alone for a critical claim.

Playbook candidates never self-activate. Benchmark records are pinned to a
benchmark version and an exact tree, and a benchmark score promotes nothing on
its own.
