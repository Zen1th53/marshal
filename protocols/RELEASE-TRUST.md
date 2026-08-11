# RELEASE-TRUST.md

## Release Gate

```text
tests/conformance
→ schema validation
→ deterministic inventory
→ hash manifest
→ provenance statement
→ owner/CI signing
→ independent verification
→ publish
```

## Trust Claims

Allowed:
- manifest hash verified,
- signature verified against named trust root,
- provenance parsed and policy accepted.

Not allowed:
- "secure release" because ZIP has SHA-256,
- "signed" when no external signature exists,
- "SLSA compliant" without level requirements being verified.
