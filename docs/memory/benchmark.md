# MARSHAL Coding Memory Benchmark

## Methodology
The benchmark measures retrieval quality, stale decision suppression, and security isolation specifically for software engineering multi-agent workflows.

### Baseline Comparisons
- **No-Memory**: Zero prior recall context.
- **Lexical-Only**: BM25 keyword matching without graph or temporal resolution.
- **MARSHAL Hybrid**: Dense vector + BM25 + Temporal Code Graph + RRF Fusion + Supersession Finalization.

### Metrics
- **Recall@k**: Fraction of relevant decisions retrieved in top-k results.
- **MRR (Mean Reciprocal Rank)**: Position of first true architectural fact.
- **Stale Suppression Rate**: Percentage of superseded / outdated patterns correctly filtered out.
- **ACL Isolation Rate**: 100% strict cross-tenant boundary preservation.
