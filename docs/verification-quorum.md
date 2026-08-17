# Verification quorum

T05 evaluates immutable attestations against a canonical change ID and content
digest. A requirement is satisfied only by eligible PASS attestations; the same
principal cannot count twice, even when provider labels differ. A VETO blocks
the evaluation.

The evaluator is a pure decision path. `EvaluateAuthorized` requires an
explicit authority adapter before a caller may use the result at a privileged
boundary; an attestation or PASS result is not authority by itself.

The durable schema stores bounded references in `verification_attestations`
(schema v25). It does not store prompts, provider output, or secret material.

The gate adapter maps a satisfied evaluation to a `gate.CheckStatusPass` result.
Missing, stale, malformed, vetoed, or unavailable inputs fail closed.

Operational projections use the existing bounded `evidence.MetricsRecorder`
with the `quorum` operation. Events carry change, principal, evidence, and
content-digest references only. Metrics and events are observational and never
authorize a transition.
