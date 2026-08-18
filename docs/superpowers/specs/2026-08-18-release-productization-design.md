# MARSHAL Release Productization Design

## Objective

Turn the completed MARSHAL implementation into a defensible public release
without changing its runtime architecture. The release must make repository
truth easy to discover, provide a verified Linux installation path, and bind
published artifacts to checksums, an SBOM, source state, and GitHub build
provenance.

## Release decision

The first unified MARSHAL product release is `v1.0.0-rc.1`.

All planned T01-T55 epics are merged, but the repository has never published a
unified MARSHAL SemVer release or verified its binary distribution path from a
public GitHub Release. An RC communicates that the implementation is feature
complete while leaving room to validate installation, compatibility, and
provider behavior before making a stable `v1.0.0` compatibility promise.

MARSHAL uses distinct version domains:

- Product version: SemVer Git tags and `marshal version` output.
- Pack version: `6.0.0`, the repository distribution/specification contract.
- Runtime specification version: `1.0.0`.
- Database schema: internal migration version `67`; users should rely on the
  migration mechanism rather than pinning this number.
- MCP protocol: `2026-07-28`.
- A2A protocol: `1.0.0`, wire header `1.0`.

Historical `runtime-v0.x` releases remain unchanged and retain their documented
Apache-2.0 grants. The new release is AGPL-3.0-only with the existing commercial
licensing option.

## Public documentation

`README.md` becomes the landing page and contains only verified claims. It links
to a new `docs/README.md` portal. Existing document paths remain stable; the
release pass adds focused installation, operations, supply-chain, release, and
example documents rather than moving the entire tree.

The provider matrix separates implemented adapters from fresh external E2E
verification. Historical evidence may be linked, but unavailable credentials or
quota are never represented as current PASS results.

## Version reporting

A small `internal/version` package owns build metadata. Local builds report
`dev`; release builds inject the SemVer tag, commit SHA, and UTC build time with
Go linker flags. `marshal version`, `marshal --version`, and JSON output expose
the same bounded fields.

## Release pipeline

A tag-triggered GitHub Actions workflow builds only supported Linux targets:

- `marshal_v1.0.0-rc.1_linux_amd64.tar.gz`
- `marshal_v1.0.0-rc.1_linux_arm64.tar.gz`

Archives include the binary, `LICENSE`, `LICENSING.md`, and a concise install
document. The workflow creates SHA-256 checksums, a CycloneDX module SBOM, and a
GitHub build-provenance attestation. It publishes release notes from a tracked
file and never embeds a private signing key.

## CI and repository health

CI adds formatting and build checks while retaining vet, tests, race tests,
conformance suites, and pack verification. Separate least-privilege workflows
provide CodeQL and dependency review. Dependabot tracks Go modules and GitHub
Actions. Local Markdown link, secret-pattern, release-package, and SBOM checks
are executable without repository secrets.

## Publication gate

No tag is pushed until all local checks and the release-preparation PR checks
pass. After publication, a fresh clone must complete the documented source
install and quick-start smoke test. At least one downloaded release archive must
match `checksums.txt`, run `marshal version`, initialize a disposable project,
and complete `marshal doctor`. GitHub attestation verification is reported only
if the API and CLI confirm it.

## Known release-candidate limitations

- Linux is the supported runtime platform; no macOS or Windows artifacts are
  published.
- External provider authentication and quota are operator-controlled. Adapter
  implementation does not imply a fresh real-provider E2E PASS.
- Bubblewrap enforcement depends on Linux kernel and distribution support.
- GitHub-hosted provenance establishes build origin, not independently verified
  bit-for-bit reproducibility.
