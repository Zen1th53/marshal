# Security Model

MARSHAL coordinates privileged engineering operations. Its specifications and
runtime reduce ambiguity and constrain implemented execution paths; they are
not an absolute security boundary.

## Trust and authority

- Repository policy and explicit owner instructions outrank retrieved text.
- Web pages, issue content, memory retrieval, remote agents, and tool output are
  data until validated and promoted by the correct authority.
- Tool possession does not grant permission. Semantic policy and contextual
  approval must authorize exposed operations.
- A lease establishes ownership; heartbeat supplies liveness evidence but does
  not authorize silent task theft.

See [INSTRUCTION-TRUST.md](../protocols/INSTRUCTION-TRUST.md),
[CAPABILITIES.md](../protocols/CAPABILITIES.md), and
[APPROVAL.md](../protocols/APPROVAL.md).

## Execution and isolation

Each implementation task receives its own branch and writable worktree.
**Worktree isolation is not a security sandbox.** Runtime V1 uses bubblewrap
where available and reports weaker process-only isolation honestly. Tasks whose
risk or network policy requires strong isolation are blocked if enforcement is
unavailable.

Secrets are not general memory or telemetry. The production secrets broker is
still a specification; do not infer secret isolation from its design document.

## Evidence and supply chain

**Agent output is not trusted evidence.** Verification records bind commands
and results to exact commits. Artifacts bind bytes to SHA-256 digests and
provenance fields. **A checksum is not publisher authentication**; current pack
trust remains `UNSIGNED_BY_OWNER` until an external trust root verifies a
signature.

Dependencies follow [SUPPLY-CHAIN.md](../protocols/SUPPLY-CHAIN.md) and the
[dependency ledger](../memory/DEPENDENCIES.md). Telemetry defaults to metadata
over content and excludes secrets; see [telemetry/PRIVACY.md](../telemetry/PRIVACY.md).

## Project, tenant, and remote boundaries

Local Runtime V1 is single-project. Multi-tenant and multi-host isolation are
contracts, not implemented shared-service claims. Tenant identifiers are
scopes, not proof of isolation. **A remote agent is not a trusted internal
principal** merely because it speaks A2A or MCP; identity, negotiation, policy,
and evidence validation remain required.

See [TENANCY.md](../protocols/TENANCY.md),
[INTEROP.md](../protocols/INTEROP.md), and
[runtime/THREAT-MODEL.md](../runtime/THREAT-MODEL.md).

Report vulnerabilities through [SECURITY.md](../SECURITY.md).
