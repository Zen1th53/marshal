# Release supply chain

Official MARSHAL releases are built by the repository's GitHub Actions release
workflow from a pushed `v*` tag. The workflow runs the release gate, builds
Linux amd64 and arm64 archives, produces SHA-256 checksums, and emits a
CycloneDX module SBOM and build metadata.

## Verify a download

Download the archive and `checksums.txt` from the same GitHub Release:

```bash
sha256sum -c --ignore-missing checksums.txt
```

For GitHub artifact attestations, install a current GitHub CLI and run:

```bash
gh attestation verify marshal_1.0.1_linux_amd64.tar.gz \
  --repo Zen1th53/marshal
```

Repeat for the SBOM, checksum file, or arm64 archive as needed. An attestation
binds an artifact to a GitHub workflow and repository; it is build provenance,
not a claim that the source or resulting binary is vulnerability-free.

## Build properties

- Release binaries use `CGO_ENABLED=0`, `-trimpath`, and no embedded VCS dirt.
- The tag, commit, and UTC build timestamp are injected into `marshal version`.
- No private signing key is stored in the repository.
- Workflow permissions are limited to contents publication, OIDC, and artifact
  attestations for the tag-triggered release job.

The project does not currently claim independently verified reproducible builds.
