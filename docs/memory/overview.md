# MARSHAL Memory Subsystem Architecture (T77–T162)

## Overview

The MARSHAL Memory Subsystem provides a multi-tier, security-governed, fail-closed institutional memory architecture for autonomous coding agents.

## Core Architectural Invariants

1. **Canonical Truth vs Derived Indexes**:
   - `memory_records_v2` in SQLite is the sole canonical source of truth.
   - Vector indexes (SQLiteVec, TurboVec), lexical FTS5, and code graphs are disposable derived projections that can be deleted and deterministically rebuilt with 100% parity.

2. **Fail-Closed Authorization & Authority Hierarchy**:
   - `AuthorityOperator` / `AuthorityPolicy` (100) > `AuthorityVerified` (80) > `AuthorityAgent` (40) > `AuthorityUnverified` (20).
   - Untrusted providers and agents cannot self-promote candidate memories to durable facts or override security policies.
   - Direct-ID guess defense enforces tenant and principal scope isolation at every step.

3. **Working Memory & Graduation Bridge**:
   - Working memory scratchpad provides private and task-shared slots with Compare-And-Swap (CAS) revision control.
   - Graduation bridge rejects refuted or falsified hypotheses from durable institutional memory.

4. **Multi-Track Adaptive Retrieval & Bounded Cache**:
   - Classifies queries into exact code symbol, procedural workflow, environment gotcha, and semantic lookup tracks.
   - Bounded retrieval cache (`T163`) reduces repeated query latency with instant scope-level invalidation on any write or mutation.
   - Grounded evidence plan compiler (`T148`, `T164`) produces prompt-safe XML `<grounded_evidence_plan>` traces with entity encoding and delimiter armor against prompt injection breakouts.

5. **Security & Cryptographic Custody**:
   - HMAC/SHA-256 signed mutation envelopes enforce linear revision chains.
   - AES-GCM-256 envelope encryption at rest with Authenticated Associated Data (AAD) bound to `MemoryID:Revision:ScopeID`.
   - Active sycophancy defense and prompt injection sanitization.

6. **Conformance & External Benchmarks**:
   - Evaluated across coding benchmarks (LoCoMo, LongMemEval, LongMemEval-V2, BEAM, EvoMemBench).
   - Specialized safety benchmarks: FAMA (forgetting quality), GateMem (isolation), PASB (sycophancy defense), and MemSyco (policy dominance).
   - Derived index destruction and rebuild parity verified at 100% deterministic fidelity (`derived-index-rebuild-report.json`).
