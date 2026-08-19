# MARSHAL Architecture

MARSHAL separates durable engineering authority from the coding-agent vendor that performs work. This page provides the authoritative runtime architecture map and component interaction specification.

---

## Implemented Runtime Architecture

```mermaid
flowchart TD
    subgraph S1["1 · ENTRY POINTS"]
        CLI["marshal CLI<br/><small>Native commands &amp; local management</small>"]
        MCP["MCP HTTP server<br/><small>protocol 2026-07-28 · Bearer Auth</small>"]
        A2A["A2A HTTP+JSON server<br/><small>accepts wire protocol 1.0 / 1.0.0</small>"]
    end

    subgraph S2["2 · RUNTIME / CONTROL PLANE"]
        DAEMON["Local daemon API<br/><small>HTTP/JSON over .marshal/runtime.sock<br/>socket mode 0600</small>"]
        RUNTIME["app.Runtime<br/><small>orchestration boundary</small>"]
        PREEXEC["Run pre-execution checks<br/><small>risk assessment · GateEngine · policy</small>"]
        COORD["Task / session coordination<br/><small>atomic claim + lease · heartbeat · Finalize</small>"]
        EVENTS["events.Engine"]
    end

    subgraph S3["3 · RUNTIME.RUN TASK EXECUTION"]
        WT["worktree.Manager.Prepare<br/><small>.marshal/worktrees/&lt;task&gt;</small>"]
        RESOLVE["resolveAdapter + Probe<br/><small>Codex · OpenCode · Gemini · Claude</small>"]
        SANDBOX["Sandbox wrapper<br/><small>When bwrap is available: worker.NewSandboxed<br/>worktree bind · read-only binds · tmpfs runtime dirs<br/>--unshare-net when network isolation is required</small>"]
        ADAPTER_RUN["provider Adapter.Run<br/><small>builds provider execution command</small>"]
        RUNNER["ProcessRunner<br/><small>worker.Manager · timeout · cancel · bounded output</small>"]
    end

    subgraph S3_VERIFY["SEPARATE VERIFICATION API"]
        VERIFY["Runtime.Verify<br/><small>CLI: marshal verify [-- cmd args...]<br/>HTTP: POST /v1/verify<br/>authorize → worker.Manager.Run(exact argv)<br/>returns exit status + SHA-256(stdout|stderr)</small>"]
    end

    subgraph S4["4 · PROVIDER PROCESSES"]
        P_CODEX["codex CLI<br/><b>REAL-E2E-VERIFIED</b>"]
        P_OPENCODE["opencode + Ollama<br/><b>REAL-E2E-VERIFIED</b>"]
        P_GEMINI["gemini CLI<br/><i>PROBED / AVAILABLE</i>"]
        P_CLAUDE["claude CLI<br/><i>PROBED / AVAILABLE</i>"]
    end

    subgraph S5["5 · RESULT HANDLING &amp; PROVENANCE EVIDENCE"]
        SANITIZE["sanitizeProviderOutput<br/><small>credential-boundary redaction (stdout / stderr)</small>"]
        ART["artifact.Store.Put<br/><small>SHA-256 content-addressed reports<br/>.marshal/artifacts/sha256/&lt;hex&gt;</small>"]
        INSPECT["Inspect Task Worktree<br/><small>commit dirty changes under policy<br/>reject success with no new commit</small>"]
        EVID["recordRunEvidence<br/><small>command/output/env evidence<br/>raw I/O bound by SHA-256 digests</small>"]
        FINISH["FinishRun → ObserveHEAD → FinalizeExecution"]
    end

    subgraph S6["6 · CANONICAL LOCAL PERSISTENCE"]
        SQLITE[("SQLite v67 — .marshal/state.db<br/><small>canonical live coordination state · task leases · audit ledger · evidence graphs</small>")]
    end

    %% Wiring
    CLI --> DAEMON
    DAEMON --> RUNTIME
    MCP --> RUNTIME
    A2A --> RUNTIME

    RUNTIME --> PREEXEC
    RUNTIME --> COORD
    RUNTIME --> EVENTS

    RUNTIME --> WT
    RUNTIME --> VERIFY

    WT --> RESOLVE
    RESOLVE --> SANDBOX
    SANDBOX --> RUNNER
    WT --> ADAPTER_RUN
    ADAPTER_RUN --> RUNNER

    RUNNER -->|"provider command"| P_CODEX
    RUNNER -->|"provider command"| P_OPENCODE
    RUNNER -->|"provider command"| P_GEMINI
    RUNNER -->|"provider command"| P_CLAUDE

    P_CODEX --> SANITIZE
    P_OPENCODE --> SANITIZE
    P_GEMINI --> SANITIZE
    P_CLAUDE --> SANITIZE

    SANITIZE --> ART
    SANITIZE --> INSPECT
    SANITIZE --> EVID

    ART --> FINISH
    INSPECT --> FINISH
    EVID --> FINISH

    FINISH --> SQLITE
    EVENTS -.-> SQLITE
    VERIFY -.-> SQLITE
```

> **Source-Faithful Implementation Guarantee**: Faithful to runtime `1.0.0` / SQLite schema `v67` at source snapshot `8f7d092e038e`. Roadmap-only or contract-only components are intentionally omitted.

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
5. **Finalization**: `FinishRun` updates HEAD observations, finalizes execution status, and commits canonical live coordination state to SQLite (`v67`).
