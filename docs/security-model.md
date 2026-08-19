# Security Model

MARSHAL coordinates privileged engineering operations. Its specifications and
runtime enforce strict, fail-closed isolation across local CLI, MCP, and A2A interfaces.

## Trust and Authority

- Repository policy and explicit owner instructions outrank retrieved text.
- Web pages, issue content, memory retrieval, remote agents, and tool output are
  untrusted data until validated and promoted by an authorized principal.
- Tool possession does not grant permission. Semantic policy and contextual
  approval must authorize exposed operations (`Allow: true` required).
- A lease establishes task ownership; heartbeat supplies liveness evidence but does
  not authorize silent task theft.

See [INSTRUCTION-TRUST.md](../protocols/INSTRUCTION-TRUST.md),
[CAPABILITIES.md](../protocols/CAPABILITIES.md), and
[APPROVAL.md](../protocols/APPROVAL.md).

## Execution and Isolation

- **Process Sandboxing**: Tasks run inside Linux `bubblewrap` mount and network namespaces with read-only root filesystems and isolated temporary mounts.
- **Resource Governance**: Enforcement of CPU limits, memory quotas, process count bounds, and a maximum 500MB worktree disk budget.
- **Process Group Escalation**: Worker processes that exceed deadlines are terminated via process group `SIGTERM` followed by `SIGKILL`.
- **Fail-Closed Fallback**: If bubblewrap is unavailable, only low-risk (R1) tasks with explicit network access may use process-only mode; high-risk (R2/R3) or network-denied tasks fail closed.

## Protocol and Control Plane Security

- **Local-Only Interfaces**: MCP and A2A servers are strictly restricted to loopback addresses (`127.0.0.1`, `::1`, `localhost`). Non-loopback binds fail closed (`ErrInsecureRemoteBind`).
- **Capability Tokens**: Bearer tokens are validated using constant-time comparisons and require explicit capability grants (`task:run`, `task:claim`, `mcp:read`, `a2a:send`).
- **Role Spoofing Prevention**: Remote agents connecting via A2A cannot self-assign internal administrative or orchestrator roles.

## Storage and Evidence Integrity

- **Append-Only Audit Events**: State mutations record immutable audit events with monotonic revisions.
- **Content-Addressed Artifacts**: Artifacts are stored under SHA-256 digests with reference tracking and garbage collection.
- **Quorum Merge Gate**: High-risk task merges require signed multi-party attestations from QA and AppSec roles before merge authorization.
- **Backup & Restore Preflight**: Online SQLite backups (`VACUUM INTO`) require preflight integrity and schema checks before restoration.

Report vulnerabilities through [SECURITY.md](../SECURITY.md).
