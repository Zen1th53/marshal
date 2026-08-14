# MARSHAL Clean-Break Rename Design

## Objective

Rename the complete product identity to MARSHAL with no compatibility aliases.
The only repository, module, executable, configuration, runtime, protocol, and
documentation identity after migration is `marshal` or `MARSHAL`, according to
the surface's naming convention.

## Product Positioning

The README opens with MARSHAL and the tagline "Vibe coding, without
Vulnerability-as-a-Service." Supporting copy presents MARSHAL as a
security-first runtime, control plane, policy engine, and verification layer for
agentic software engineering. It emphasizes isolation, least privilege, scoped
permissions, approvals, deterministic verification, evidence, auditability,
reproducibility, vendor neutrality, and controlled autonomy.

The closing brand line is "Move fast. Grant less. Verify everything."

## Identity Migration

- Product display name: `MARSHAL`
- CLI and command directory: `marshal`, `cmd/marshal`
- Go module and imports: `github.com/Zen1th53/marshal`
- Repository: `github.com/Zen1th53/marshal`
- Environment prefix: `MARSHAL_`
- Runtime and state directory: `.marshal/`
- Runtime sandbox home: `/home/marshal`
- Task branch prefix: `marshal/`
- MCP server and tool identifiers: `marshal-*` and `marshal_*`
- Authentication token prefix and acquisition evidence schemas/archive paths:
  `marshal_*` and `marshal-*`

All tracked code, tests, fixtures, schemas, manifests, GitHub metadata, release
documents, legal documents, examples, badges, and URLs adopt this identity.
There is no legacy CLI, environment variable, runtime path, protocol name, or
other compatibility shim.

## Runtime State

The application discovers and creates only `.marshal/`. The ignored local state
found during preflight is moved intact to `.marshal/` before final validation.
The source directory is then absent. The database, artifacts, logs, worktrees,
socket, and PID paths retain their relative structure beneath `.marshal/`.
Nothing from local runtime state is staged or committed.

## Implementation Boundaries

Behavioral contracts are changed test-first for layout paths, CLI output,
environment variables, sandbox paths, MCP identifiers, token formats, evidence
schemas, and task branch names. Mechanical module/import and documentation
updates follow those tests. Tracked distribution metadata is regenerated using
the repository's release tooling rather than hand-edited hashes.

No dependencies are upgraded. No unrelated code is refactored or reformatted.
Historical Git objects and tag names are not rewritten; the checked-out HEAD
and worktree are the audit scope.

## Verification

Verification includes Go formatting, build, unit/integration tests, vet, race
tests, all Python unittest suites, pack validation, release-manifest
verification, CLI help/init/doctor smoke tests, diff whitespace checks, tracked
and hidden-worktree zero-occurrence searches, named-path searches, and a staged
secret/private-state audit.

External provider tests run only when their binaries and credentials are
available; every skipped external test is reported explicitly.

## GitHub Migration

After local verification, authenticate as `Zen1th53`, inspect the installed
GitHub CLI/API syntax, rename the repository to `marshal`, set the exact required
description, replace topics with the approved focused topic set, update `origin`
to the canonical repository, verify fetch and push access, push the migration,
and inspect the resulting Actions run without bypassing branch protection.
