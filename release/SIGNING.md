# Signing and Verification

## Preferred Production Options

Depending on environment:
- keyless/key-backed Sigstore/Cosign,
- organization-managed signing key,
- minisign,
- GPG.

The trust root must be distributed independently enough to be meaningful.

## Pack Rule

A signing key shipped in the same ZIP as the artifact does not establish
publisher trust.

Therefore this generated pack is intentionally marked:

```text
UNSIGNED_BY_OWNER
```

until the owner signs the final artifact/attestation.

## Cosign Example

For an OCI artifact/attestation, use current Cosign attestation/signature
workflows and verify identity/key according to organization policy.

Do not copy example key paths into production policy.
