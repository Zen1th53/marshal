# Security policy

## Scope and supported versions

Security issues include defects in runtime authority, policy or approval
enforcement, task ownership, sandbox/worktree handling, secret boundaries,
adapter execution, MCP/A2A authentication, evidence, artifacts, migrations,
release tooling, or unsafe security documentation.

The latest MARSHAL product release and `main` receive security fixes. The
`v1.0.0-rc.1` line is supported until superseded. Historical `runtime-v0.x`
tags and downstream modifications are not independently supported.

## Report privately

Do not disclose exploit details, credentials, private source, or proof-of-
concept attacks in a public issue. Use GitHub private vulnerability reporting.
If that channel is unavailable, contact `extreme29@proton.me`.

Include the affected version/commit and environment, impacted boundary,
prerequisites, reproducible steps, expected and observed behavior, and likely
impact. Use synthetic data; never send live secrets or third-party data.

This policy does not authorize testing against systems, accounts, data, or
infrastructure you do not own or have explicit permission to test. Report
upstream provider or dependency defects to that project when they are not
specific to MARSHAL.

## Coordinated disclosure

Allow reasonable time to reproduce, assess, fix, and coordinate a release.
Material status changes and disclosure timing will be communicated in good
faith. Reporter credit is provided when requested.

## Trust assumptions and limits

- Git worktrees provide source separation, not a security sandbox. Linux
  bubblewrap supplies the worker isolation boundary.
- The host owner, kernel, filesystem, Git configuration, and operator account
  remain trusted. MARSHAL is not a hostile-host containment system.
- Network access follows configured egress policy. Provider network needs do
  not permit arbitrary task traffic.
- MCP and A2A endpoints require scoped bearer-token authentication. Deployers
  remain responsible for transport exposure and token storage.
- Provider output, tool output, fixtures, model labels, policy-test PASS, and
  historical evidence are data, not authorization.
- Sanitization is defense in depth, not permission to put secrets in prompts,
  fixtures, or logs.
- Release checksums and attestations establish artifact integrity and workflow
  provenance; they do not prove the absence of vulnerabilities.

Read the detailed [security model](docs/security-model.md) before sensitive use.
