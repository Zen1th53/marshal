# Security Policy

## Scope

A MARSHAL security issue includes a vulnerability in the executable runtime,
policy or approval enforcement, task ownership, sandbox/worktree handling,
secrets or memory controls, adapter execution, artifact provenance,
interoperability boundaries, release tooling, or documentation that would lead
operators to deploy an unsafe configuration.

The specification documents also welcome reports of concrete security
contradictions, but a missing future implementation is not by itself a runtime
vulnerability when its status is accurately documented.

## Supported versions

The project currently has no tagged release series. Security fixes are assessed
against the `main` branch and the current documented local runtime version.
Older commits and downstream modifications are not independently supported.

## Reporting privately

Do not disclose exploit details, credentials, private repository content, or
proof-of-concept attacks in a public issue.

Use GitHub's private vulnerability reporting when enabled for this repository.
Otherwise contact the project maintainer through an explicitly published
private security contact. If neither private channel is available, open a
minimal public issue asking the maintainer to establish one; include no
vulnerability details.

Useful reports include:

- affected commit, version, and environment;
- impacted trust boundary or asset;
- prerequisites and reproducible steps;
- expected and observed behavior;
- impact and likely scope;
- suggested mitigation, if known.

Do not include live secrets or data belonging to others.

## Coordinated disclosure

Allow maintainers reasonable time to reproduce, assess, fix, and coordinate a
release before public disclosure. Maintainers should acknowledge receipt,
communicate material status changes, credit reporters who request credit, and
coordinate disclosure timing in good faith.

This policy does not authorize testing against systems, accounts, data, or
infrastructure you do not own or have explicit permission to test. Vendor-agent
or dependency vulnerabilities should also be reported to the affected upstream
project when the defect is not specific to MARSHAL.
