# MARSHAL Memory Provenance, Evidence and Trust Binding

**Specification:** T83 — Provenance, Evidence & Trust Binding

## 1. Evidence Binding Principles

1. **Reference, Never Duplicate**: Memory records reference artifact/attestation `EvidenceIDs` rather than inlining large raw evidence.
2. **Repository Provenance**: Repository facts bind to `head_commit`, `branch_name`, and `worktree_id`.
3. **Actor Provenance**: Execution and assertion facts record `source.agent_id`, `session_id`, and `run_id`.

## 2. Derived Trust Scoring Formula

Trust is deterministically derived from structured properties, **never** accepted from LLM self-reports:

$$\text{Trust} = 0.40 \cdot \text{AuthorityScore} + 0.35 \cdot \text{EvidenceScore} + 0.25 \cdot \text{FreshnessScore}$$

- **AuthorityScore**: Operator (1.0) > Policy (0.90) > Verified Consensus (0.80) > Single Agent (0.40).
- **EvidenceScore**: $\ge 2$ evidence items (1.0), 1 item (0.75), 0 items (0.20).
- **FreshnessScore**: Exponential time decay with a 30-day half-life: $e^{-\Delta t / 720\text{h}}$.

## 3. Invariants

- Protected records (`decision`, `finding`, `failure`) require verifiable `EvidenceIDs` to achieve durable promotion.
- Stale memory decays in trust score and is outranked by fresh verified observations.
