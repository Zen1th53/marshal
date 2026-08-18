# MARSHAL v1.0.0-rc.1

MARSHAL is a security-first local control plane for agentic coding. It keeps
task ownership, model execution, policy, approval, verification, and evidence
as separate operations instead of treating model output as authority.

This release candidate is the first unified public product release after the
T01-T55 implementation series. It establishes the binary installation and
compatibility contract that will be validated before v1.0.0.

## Highlights

- Durable task DAGs, ownership leases, recovery, and multi-agent scheduling
- Git worktree isolation with fail-closed Linux bubblewrap execution
- Role, capability, policy, approval, egress, and destructive-operation gates
- Codex, Claude Code, Gemini CLI, and OpenCode process adapters
- Verification quorum, policy conformance, evidence, provenance, and artifacts
- Authenticated MCP 2026-07-28 and A2A 1.0 interfaces
- Local daemon, SQLite schema 67, diagnostics, logs, cancellation, and recovery

Provider adapters are implemented and contract-tested. Fresh real-provider E2E
depends on operator credentials, quota, installed binaries, and model tool
capability; unavailable external services are not reported as release PASS.

## Install

```bash
curl -LO https://github.com/Zen1th53/marshal/releases/download/v1.0.0-rc.1/marshal_1.0.0-rc.1_linux_amd64.tar.gz
curl -LO https://github.com/Zen1th53/marshal/releases/download/v1.0.0-rc.1/checksums.txt
sha256sum -c --ignore-missing checksums.txt
tar -xzf marshal_1.0.0-rc.1_linux_amd64.tar.gz
install -Dm755 marshal "$HOME/.local/bin/marshal"
marshal version
```

Then enter a Git repository and run:

```bash
marshal init
marshal doctor
marshal daemon
```

See [Getting started](https://github.com/Zen1th53/marshal/blob/v1.0.0-rc.1/docs/getting-started.md)
for task import, provider execution, and evidence inspection.

## Artifacts and verification

- `marshal_1.0.0-rc.1_linux_amd64.tar.gz`
- `marshal_1.0.0-rc.1_linux_arm64.tar.gz`
- `marshal_1.0.0-rc.1_sbom.cdx.json`
- `build-metadata.json`
- `checksums.txt`

Every asset has a SHA-256 checksum. GitHub build provenance is emitted by the
tag-triggered release workflow. Verify it with:

```bash
gh attestation verify marshal_1.0.0-rc.1_linux_amd64.tar.gz --repo Zen1th53/marshal
```

## Security and trust

MARSHAL reduces authority and supply-chain risk; it does not make a model or
host inherently trusted. Git worktrees are not a sandbox, historical evidence
is not authorization, and an expected or actual ALLOW does not execute a
protected action. Read [SECURITY.md](https://github.com/Zen1th53/marshal/blob/v1.0.0-rc.1/SECURITY.md).

## Known limitations

- Linux amd64 and arm64 are the supported release targets.
- Strong worker isolation requires bubblewrap and a compatible Linux host.
- External providers retain their own authentication, quota, availability, and
  data-handling boundaries.
- This RC does not claim independently verified reproducible builds.

There is no database upgrade action required beyond MARSHAL's supported forward
migration; back up `.marshal/` before upgrading. See the
[full changelog](https://github.com/Zen1th53/marshal/blob/v1.0.0-rc.1/CHANGELOG.md)
and [licensing terms](https://github.com/Zen1th53/marshal/blob/v1.0.0-rc.1/LICENSING.md).
