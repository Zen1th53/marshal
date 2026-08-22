# MARSHAL Memory Subsystem Architecture (T77–T164)

## Overview

The MARSHAL Memory Subsystem provides a multi-tier, security-governed institutional memory architecture for autonomous coding agents, evaluated with reproducible internal and external-compatible benchmarks.

See [Runtime Memory Lifecycle](runtime-lifecycle.md) for the production task-start recall, bounded admission, terminal capture, utility attribution, and retrieval receipt behavior.

## Core Architectural Invariants

1. **Canonical Truth vs Derived Indexes**:
   - `memory_records_v2` in SQLite is the sole canonical source of truth.
   - Vector indexes (SQLiteVec, TurboVec), lexical FTS5, and code graphs are disposable derived projections that can be deleted and deterministically rebuilt from SQLite with 100% parity on tested benchmark fixtures (`derived-index-rebuild-report.json`).

2. **Fail-Closed Authorization & Authority Hierarchy**:
   - `AuthorityOperator` / `AuthorityPolicy` (100) > `AuthorityVerified` (80) > `AuthorityAgent` (40) > `AuthorityUnverified` (20).
   - Untrusted providers and agents cannot self-promote candidate memories to durable facts or override security policies.
   - Direct-ID guess defense enforces tenant and principal scope isolation at every step.

3. **Working Memory & Graduation Bridge**:
   - Working memory scratchpad provides private and task-shared slots with Compare-And-Swap (CAS) revision control.
   - Graduation bridge rejects refuted or falsified hypotheses from durable institutional memory.

4. **Multi-Track Adaptive Retrieval, Bounded Cache & Delimiter Armor**:
   - Classifies queries into exact code symbol, procedural workflow, environment gotcha, and semantic lookup tracks.
   - Bounded retrieval cache (`T163`) reduces repeated query p95 latency by ~70% on tested fixtures with instant scope-level invalidation on mutation.
   - Grounded evidence plan compiler (`T148`, `T164`) constructs XML `<grounded_evidence_plan>` blocks with entity encoding to prevent prompt delimiter breakout attacks.

5. **Security & Cryptographic Custody**:
   - HMAC/SHA-256 signed mutation envelopes enforce linear revision chains.
   - AES-GCM-256 envelope encryption at rest with Authenticated Associated Data (AAD) bound to `MemoryID:Revision:ScopeID`.
   - Active sycophancy defense and prompt injection sanitization.

6. **Conformance & External Benchmarks**:
   - Evaluated across coding benchmark suites (LoCoMo-compatible, LongMemEval-compatible, BEAM-compatible, EvoMemBench).
   - Specialized safety benchmarks: FAMA (forgetting quality), GateMem (isolation), PASB (sycophancy defense), and MemSyco (policy dominance).
   - Derived index destruction and rebuild parity verified at 100.0% deterministic fidelity on tested fixtures (`derived-index-rebuild-report.json`).

## Known Limitations

- **Live External Provider E2E**: Adapter protocol conformance passed across all adapters, but live external API network E2E was **NOT_RUN** due to unavailable API credentials in the audited environment.
- **Semantic Prompt Injection Scope**: Delimiter escaping prevents XML tag breakout, but does not eliminate all semantic prompt injection classes; authorization, treating retrieved content as data, and current policy enforcement remain required.
- **Benchmark Specificity**: Benchmark scores are specific to the evaluated harness and scenarios on Linux amd64; they do not imply identical performance on arbitrary unseen workloads.
