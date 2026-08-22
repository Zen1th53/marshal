# MARSHAL Architecture

MARSHAL separates durable engineering authority from the coding-agent vendor that performs work. This page provides the authoritative runtime architecture map and component interaction specification.

---

## Implemented Runtime Architecture

<p align="center">
  <img src="assets/marshal-architecture-graphite.svg"
       alt="MARSHAL implemented runtime architecture"
       width="100%">
</p>

> Source-faithful to runtime `1.0.0` / SQLite schema `v70` at source snapshot
> `8f7d092e038e`. Roadmap-only or contract-only components are intentionally omitted.

---

## 1. Entry Surfaces (`cmd/internal`)

The MARSHAL control plane exposes three interoperable entry surfaces:
- **Native CLI (`marshal`)**: Direct command execution and administrative management.
- **MCP HTTP Server (`protocol 2026-07-28`)**: Model Context Protocol endpoint secured by HMAC Bearer tokens.
- **A2A HTTP+JSON Server (`protocol 1.0`)**: Agent-to-Agent wire protocol exposing agent card discovery and task delegation.

All entry points communicate with `app.Runtime` either in-process or over the local Unix domain socket (`.marshal/runtime.sock`, permissions `0600`).

---

## 2. Runtime Control Plane (`app.Runtime`)

The central coordination engine performs:
- **Pre-execution checks**: Risk descriptor evaluation (`R0`..`R3`), capability authorization, and security officer veto validation.
- **Task & Session Coordination**: Atomic task claims, lease heartbeats, and transactional status transitions.
- **Events Engine**: Real-time event streaming and append-only audit recording.
- **Explicit Verification API**: Dedicated `Runtime.Verify` endpoint authorizing `worker.Manager.Run` with exact command arguments and recording SHA-256 digests of stdout/stderr.

---

## 3. Isolated Task Execution (`Runtime.Run`)

Task runs follow a strict, fail-closed isolation pipeline:
1. **Worktree Preparation**: `worktree.Manager.Prepare` allocates an isolated git worktree under `.marshal/worktrees/<task-id>`.
2. **Adapter Resolution & Probing**: Resolves the configured provider binary (`Codex`, `OpenCode + Ollama`, `Gemini`, `Claude`) and verifies its capability state.
3. **Command Construction**: `providerAdapter.Run` builds the execution command.
4. **Sandboxed Process Supervisor**: `worker.Manager` enforces timeouts (default 30m), output limits, and heartbeat tracking. When Linux kernel namespaces are available, `worker.NewSandboxed` wraps the process in an unprivileged `bubblewrap` (`bwrap`) container with read-only root mounts, tmpfs runtime directories, and `--unshare-net` egress isolation.

---

## 4. Result Handling & Persistence

Upon process termination:
1. **Sanitization**: Output streams are filtered and credential boundary redaction is applied.
2. **Artifact Ingestion**: Reports are stored in `.marshal/artifacts/sha256/<hex>` with content-addressed SHA-256 digests.
3. **Worktree Inspection**: Git working trees are inspected; changes are committed under policy, or uncommitted runs are rejected.
4. **Evidence Recording**: Command, environment, and output evidence nodes are recorded with cryptographic binding.
5. **Finalization**: `FinishRun` updates HEAD observations, finalizes execution status, and commits canonical live coordination state to SQLite (`v70`).
