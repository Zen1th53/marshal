# Owner Signing Workflow

The pack includes `tools/detached_sign.py` for detached Ed25519 signing and
verification.

## Development / Test Key

For local testing only:

```bash
python tools/detached_sign.py generate-keypair \
  --private /secure/path/marshal-private.pem \
  --public marshal-public.pem
```

Do not put the private key into the repository or ZIP.

## Sign Manifest

```bash
python tools/detached_sign.py sign \
  --private /secure/path/marshal-private.pem \
  --file distribution/PACK-MANIFEST.json \
  --signature PACK-MANIFEST.sig
```

## Verify

```bash
python tools/detached_sign.py verify \
  --public marshal-public.pem \
  --file distribution/PACK-MANIFEST.json \
  --signature PACK-MANIFEST.sig
```

For production CI/release infrastructure, Sigstore/Cosign or an
organization-managed signing service may be preferable because key identity and
audit can be managed outside the pack.

The current generated ZIP remains `UNSIGNED_BY_OWNER` until an external owner/CI
identity signs it or its manifest/attestation.
