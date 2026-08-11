# in-toto Attestation Profile

Use in-toto Statement v1:

```json
{
  "_type": "https://in-toto.io/Statement/v1",
  "subject": [
    {
      "name": "artifact",
      "digest": {"sha256": "..."}
    }
  ],
  "predicateType": "...",
  "predicate": {}
}
```

Subjects are matched by digest.

For signing/authentication, prefer a DSSE-compatible envelope or a supported
Sigstore/Cosign attestation workflow.

Do not put the private signing key inside the pack.
