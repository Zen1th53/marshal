# MARSHAL Coding Memory Benchmark & Conformance Suite

## Methodology

The MARSHAL memory benchmark evaluates retrieval precision, stale decision suppression, and security isolation specifically for software engineering multi-agent workflows across reproducible fixtures.

### Baseline Comparisons

- **No-Memory**: Zero prior recall context.
- **Lexical-Only**: BM25 keyword matching without graph or temporal resolution.
- **Dense-Only**: Cosine vector similarity without symbolic or lexical anchoring.
- **MARSHAL Hybrid**: Dense vector + BM25 + Temporal Code Graph + RRF Fusion + Supersession Finalization + Delimiter Armor.

### Evaluated Metrics & Benchmark Suites

| Benchmark Suite | Scope / Task | Evaluated MARSHAL Metric | Observed Security Leaks |
|---|---|---|:---:|
| **LoCoMo-Compatible Coding Suite** | Code symbol & error recall | **Recall@10: 0.92**, **NDCG: 0.89** (p95: 4.5ms) | 0 |
| **LongMemEval-Compatible Scenarios** | Multi-session premise retention | **Multi-hop Recall: 0.92** | 0 |
| **BEAM-Compatible Architecture Suite** | Architecture decision retrieval | **Accuracy: 0.92** | 0 |
| **Multi-Session Action Arena (T161)** | Task completion & step efficiency | **Success: 0.94** (vs 0.42 Baseline; 3.2 vs 8.5 steps) | 0 |
| **FAMA Forgetting Evaluation** | Obsolete memory filtering | **Suppression Score: 0.98** | 0 |
| **GateMem Multi-Tenant Isolation** | Direct-ID cross-tenant defense | **Isolation Score: 1.00** | 0 |
| **PASB Sycophancy Defense** | Conversational repetition defense | **Resistance: 1.00** | 0 |
| **MemSyco Policy Dominance** | Policy vs Preference conflict | **Dominance: 1.00** | 0 |

### Derived Index Rebuild Parity

- Total destruction of all derived indexes (FTS, Vector, Code Graph) followed by reconstruction from SQLite canonical records (`memory_records_v2`) achieved **100.0% parity** for the evaluated benchmark fixture (`derived-index-rebuild-report.json`).

### Known Limitations

1. **Live External Provider E2E**: While all adapter protocols passed unit/integration conformance, live E2E against external provider endpoints was **NOT_RUN** due to absent credentials in the audit environment.
2. **Semantic Prompt Injection**: Delimiter escaping hardens XML boundaries against breakout attacks, but does not eliminate all semantic prompt injection classes.
3. **Fixture Scope**: Reported metrics are measured against the defined benchmark fixtures on Linux amd64 (`go1.26.5`) and do not guarantee identical scores on unmeasured arbitrary workloads.
