# Fault Injection Conformance

Fault tests verify fail-closed and recovery behavior without destructive real-world
actions.

Safe synthetic faults:

- policy engine unavailable,
- secret broker unavailable,
- semantic index unavailable,
- artifact store unavailable,
- duplicate event delivery,
- stale optimistic-concurrency revision,
- runtime/file split-brain,
- worker crash before handoff.

Expected principle:

```text
optional capability failure → explicit degradation
privileged control failure  → fail closed
canonical write conflict    → reject/reconcile
worker crash                → preserve evidence, do not mark complete
```
