# Release Trust

MARSHAL release trust has four layers:

```text
reproducible inputs
→ digest manifest
→ in-toto/SLSA provenance
→ external signature / identity verification
```

A checksum alone proves integrity relative to a manifest, not publisher identity.

Current pack trust status is recorded in `release/TRUST-STATUS.json`.
