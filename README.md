# MARSHAL

[![CI](https://github.com/Zen1th53/marshal/actions/workflows/ci.yml/badge.svg)](https://github.com/Zen1th53/marshal/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/Zen1th53/marshal?include_prereleases&color=blue)](https://github.com/Zen1th53/marshal/releases)
[![Go Version](https://img.shields.io/github/go-mod/go-version/Zen1th53/marshal)](https://go.dev)
[![License: AGPL v3](https://img.shields.io/badge/License-AGPL_v3-blue.svg)](LICENSE)
[![Security: bwrap](https://img.shields.io/badge/Sandbox-Bubblewrap-green.svg)](docs/security-model.md)

> Security-first agentic coding runtime for isolated, policy-enforced, and verifiable AI software engineering.

MARSHAL is a production-grade control plane and runtime for autonomous coding agents. It separates authority, process execution, security policy, and empirical verification — ensuring AI coding models execute within strict isolation boundaries, produce audit evidence, and pass verification gates before changes touch production code.

---

## Why MARSHAL?

Autonomous coding agents are powerful engineering tools, but raw AI models should never implicitly act as:
- **Planner**
- **Developer**
- **Security Officer**
- **Shell Administrator**
- **Source of Truth**

Without a dedicated control plane, AI agents leak secrets, alter host environments, bypass test suites, and overwrite git state without audit trails.

MARSHAL enforces strict operational separation:
1. **Separation of Authority**: Agents hold explicit, leased capabilities; policy engines approve execution paths.
2. **Fail-Closed Sandboxing**: Unprivileged Linux `bubblewrap` (`bwrap`) namespaces isolate filesystem and process environments.
3. **Empirical Verification**: No task is marked `PASSED` without reproducible test runs, clean git diffs, and evidence digests.
4. **Immutable Audit Trail**: Event logs, decisions, provenance chains, and artifacts are permanently cryptographically bound in SQLite (`v67`).

---

## What MARSHAL Provides

- **Fail-Closed Execution Cells**: Process supervisor operating inside unprivileged Linux `bwrap` sandboxes with worktree isolation and egress network filtering.
- **Capability Broker & Leases**: Transactional task leases (`0600` Unix socket control) preventing concurrent file collisions and unauthorized API calls.
- **Policy-as-Code Engine**: Granular security rules enforcing risk ceilings, path restrictions, and security officer approval vetoes.
- **Evidence-Derived Trust Score**: Composite 0..100 scoring engine deriving trust from verification quorum, test outputs, and risk assessments.
- **Multi-Provider Adapter Suite**: Native adapters for **Codex**, **OpenCode + Local Ollama**, **Gemini CLI**, and **Claude Code**.
- **Protocol Interoperability**: First-class support for **MCP (2026-07-28)** and **Agent-to-Agent (A2A 1.0)** with Bearer authentication.
- **Deterministic Self-Healing**: Automated recovery for stalled agent processes, state reconciliation, and crash survival.
- **Legal & Provenance Auditing**: Automated chain-of-title tracking, copyright attribution, and release verification manifests.

---

## Architecture

```mermaid
%%{init: {
  "theme": "base",
  "themeVariables": {
    "darkMode": true,
    "background": "#0d0f12",
    "primaryColor": "#161b22",
    "primaryTextColor": "#f0f6fc",
    "primaryBorderColor": "#30363d",
    "lineColor": "#484f58",
    "secondaryColor": "#111418",
    "tertiaryColor": "#0d0f12"
  }
}}%%
flowchart TD
    subgraph S1["1 · ENTRY POINTS"]
        CLI["<b>marshal CLI</b><br/><small>Native commands &amp; local management</small>"]
        MCP["<b>MCP HTTP server</b><br/><small>protocol 2026-07-28 · Bearer Auth</small>"]
        A2A["<b>A2A HTTP+JSON server</b><br/><small>accepts wire protocol 1.0 / 1.0.0</small>"]
    end

    subgraph S2["2 · RUNTIME / CONTROL PLANE"]
        DAEMON["<b>Local daemon API</b><br/><small>HTTP/JSON over .marshal/runtime.sock<br/>socket mode 0600</small>"]
        RUNTIME["<b>app.Runtime</b><br/><small>orchestration boundary</small>"]
        PREEXEC["<b>Run pre-execution checks</b><br/><small>risk assessment · GateEngine · policy</small>"]
        COORD["<b>Task / session coordination</b><br/><small>atomic claim + lease · heartbeat · Finalize</small>"]
        EVENTS["<b>events.Engine</b>"]
    end

    subgraph EXEC_ROW["3 · TASK EXECUTION &amp; VERIFICATION"]
        subgraph S3["3 · RUNTIME.RUN TASK EXECUTION"]
            WT["<b>worktree.Manager.Prepare</b><br/><small>.marshal/worktrees/&lt;task&gt;</small>"]
            ADAPTER_RUN["<b>provider Adapter.Run</b><br/><small>builds provider execution command</small>"]
            RESOLVE["<b>resolveAdapter + Probe</b><br/><small>Codex · OpenCode · Gemini · Claude</small>"]
            RUNNER["<b>ProcessRunner</b><br/><small>worker.Manager · timeout · cancel · bounded output</small>"]
            SANDBOX["<b>Sandbox wrapper</b><br/><small>When bwrap is available: worker.NewSandboxed<br/>worktree bind · read-only binds · tmpfs runtime dirs<br/>--unshare-net when network isolation is required</small>"]
        end

        subgraph S3_VERIFY["SEPARATE VERIFICATION API"]
            VERIFY["<b>Runtime.Verify</b><br/><small>CLI: marshal verify [-- cmd args...]<br/>HTTP: POST /v1/verify<br/>authorize → worker.Manager.Run(exact argv)<br/>returns exit status + SHA-256(stdout|stderr)</small>"]
        end
    end

    subgraph S4["4 · PROVIDER PROCESSES"]
        P_CODEX["<b>codex CLI</b><br/><small style=\"color:#3fb950;\">REAL-E2E-VERIFIED</small>"]
        P_OPENCODE["<b>opencode + Ollama</b><br/><small style=\"color:#3fb950;\">REAL-E2E-VERIFIED</small>"]
        P_GEMINI["<b>gemini CLI</b><br/><small style=\"color:#d29922;\">PROBED / AVAILABLE</small>"]
        P_CLAUDE["<b>claude CLI</b><br/><small style=\"color:#d29922;\">PROBED / AVAILABLE</small>"]
    end

    subgraph S5["5 · RESULT HANDLING &amp; PROVENANCE EVIDENCE"]
        SANITIZE["<b>sanitizeProviderOutput</b><br/><small>credential-boundary redaction (stdout / stderr)</small>"]
        ART["<b>artifact.Store.Put</b><br/><small>SHA-256 content-addressed reports<br/>.marshal/artifacts/sha256/&lt;hex&gt;</small>"]
        INSPECT["<b>Inspect Task Worktree</b><br/><small>commit dirty changes under policy<br/>reject success with no new commit</small>"]
        EVID["<b>recordRunEvidence</b><br/><small>command/output/env evidence<br/>raw I/O bound by SHA-256 digests</small>"]
        FINISH["<b>FinishRun → ObserveHEAD → FinalizeExecution</b>"]
    end

    subgraph S6["6 · CANONICAL LOCAL PERSISTENCE"]
        SQLITE[("<b>SQLite v67 — .marshal/state.db</b><br/><small>canonical live coordination state · task leases · audit ledger · evidence graphs</small>")]
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

    classDef default fill:#161b22,stroke:#30363d,stroke-width:1px,color:#f0f6fc;
    classDef subcard fill:#111418,stroke:#21262d,stroke-width:1px,color:#8b949e;
    classDef verified fill:#161b22,stroke:#238636,stroke-width:1px,color:#f0f6fc;
    classDef avail fill:#161b22,stroke:#9e6a03,stroke-width:1px,color:#f0f6fc;

    class PREEXEC,COORD,EVENTS subcard;
    class P_CODEX,P_OPENCODE verified;
    class P_GEMINI,P_CLAUDE avail;
```

> **Source-Faithful Implementation Guarantee**: Faithful to runtime `1.0.0` / SQLite schema `v67` at source snapshot `8f7d092e038e`. Roadmap-only or contract-only components are intentionally omitted.

For detailed technical specs, inspect [docs/architecture.md](docs/architecture.md) and [docs/concepts.md](docs/concepts.md).

---

## Current Release Status

| Property | Value |
|---|---|
| **Product Release** | **`v1.0.0`** |
| **Source Channel** | `main` |
| **Database Schema** | **`v67`** (SQLite WAL mode) |
| **MCP Protocol** | `2026-07-28` |
| **A2A Wire Version** | `1.0` |
| **Platform Support** | Linux (x86_64 / arm64) |
| **Sandbox Engine** | `bubblewrap` (`bwrap`) |

---

## Provider Support Matrix

MARSHAL probes provider binaries dynamically and tracks provider maturity across six distinct states: `IMPLEMENTED`, `INSTALLED`, `AVAILABLE`, `AUTHENTICATED`, `CAPABILITY-PROBED`, and `REAL-E2E-VERIFIED`.

| Provider Adapter | Version / Binary | Verification Status | Native CLI | MCP | A2A |
|---|---|---|:---:|:---:|:---:|
| **Codex** | `codex-cli` | **REAL E2E VERIFIED** | Yes | Yes | Yes |
| **OpenCode + Local Ollama** | `opencode` + `qwythos-9b` | **REAL E2E VERIFIED** | Yes | Yes | Yes |
| **Gemini CLI** | `gemini` | **PROBED / AVAILABLE** | Yes | Yes | Yes |
| **Claude Code** | `claude` | **PROBED / AVAILABLE** | Yes | Yes | Yes |

---

## 5-Minute Quick Start

### Prerequisites
- **OS**: Linux host (Ubuntu, Debian, Fedora, Arch, BlackArch, Alpine)
- **Dependencies**: Git, Go `1.25`+, Linux `bubblewrap` (`bwrap`)
- **Providers**: `codex-cli` (for Codex) or `opencode` + `ollama` (for local models)

### 1. Install MARSHAL

#### Option A: Install via Go
```bash
go install github.com/Zen1th53/marshal/cmd/marshal@latest
```

#### Option B: Build from Source
```bash
git clone https://github.com/Zen1th53/marshal.git
cd marshal
go build -o bin/marshal ./cmd/marshal
sudo cp bin/marshal /usr/local/bin/
```

### 2. System Health Diagnostics
```bash
# Verify CLI version and schema
marshal version

# Initialize .marshal state directory in repository
marshal init

# Run system health diagnostics & probe provider binaries
marshal doctor --probe-providers
```

### 3. Start Local Daemon
Start the daemon control plane in a background window or service:
```bash
marshal daemon
```

Verify daemon health:
```bash
marshal status
```

---

## First Task Execution Workflow

### 1. Define Task Schema (`tasks.json`)
```json
[
  {
    "id": "TASK-DEMO-001",
    "title": "Create application status endpoint",
    "status": "ready",
    "risk": "R1",
    "base_commit": "HEAD",
    "head_commit": "HEAD"
  }
]
```

### 2. Import & Register Agent
```bash
# Register an agent
marshal agent register --name OperatorAgent --role developer

# Import tasks into SQLite control plane
marshal task import tasks.json
marshal tasks
```

### 3. Execute Task with Choice of Adapter

#### Option A: Codex Execution
```bash
marshal run TASK-DEMO-001 --adapter codex
```

#### Option B: OpenCode + Local Ollama Execution
```bash
marshal run TASK-DEMO-001 --adapter opencode --model qwythos-9b
```

### 4. Inspect Execution & Audit Logs
```bash
# View execution logs, artifacts, and timeline events
marshal logs TASK-DEMO-001

# Run verification suite over changes
marshal verify -- go test ./...
```

---

## Command Reference Summary

| Command | Purpose |
|---|---|
| `marshal version` | Output MARSHAL version, commit SHA, build date, and DB schema version |
| `marshal init` | Initialize local `.marshal/` control plane directory |
| `marshal doctor [--probe-providers]` | Run system health diagnostics and provider availability probes |
| `marshal daemon` | Launch the MARSHAL control plane daemon background server |
| `marshal status` | Query active tasks, registered agents, and daemon health |
| `marshal adapters` | Display discovered provider binaries and availability states |
| `marshal adapter probe <NAME>` | Perform deep capability probe for a specific adapter |
| `marshal run <TASK-ID> --adapter <NAME>` | Execute a ready task with a specific provider adapter |
| `marshal logs <TASK-ID>` | Display execution logs, generated artifacts, and event timeline |
| `marshal cancel <TASK-ID>` | Cancel an active task execution gracefully |
| `marshal auth token create --name <NAME>` | Generate Bearer token for authenticated MCP/A2A endpoints |
| `marshal legal audit [--json]` | Perform IP provenance and chain-of-title compliance audit |

Full CLI reference available at [docs/cli.md](docs/cli.md).

---

## Security Model & Sandboxing

MARSHAL is designed from the ground up as a **security-first control plane**:
- **Unix Socket Permissions (`0600`)**: Local daemon communicates over Unix domain sockets restricted exclusively to the invoking user.
- **Fail-Closed Bubblewrap Isolation**: Task processes run inside unprivileged Linux `bwrap` mount namespaces with read-only root filesystems and explicit tmpfs mounts.
- **Secrets Boundary & Redaction**: High-entropy API tokens and private environment variables are automatically redacted from logs, events, and artifact payloads.
- **Network Egress Firewalling**: Egress network policy engines restrict outbound connections to authorized LLM endpoints.

Read the complete [Security Model](docs/security-model.md) and [SECURITY.md](SECURITY.md).

---

## Documentation Directory

Explore the complete documentation in `docs/`:

- [docs/README.md](docs/README.md) — Documentation Hub & Sitemap
- [docs/getting-started.md](docs/getting-started.md) — Step-by-step tutorial from zero to production deployment
- [docs/architecture.md](docs/architecture.md) — Complete system design and component architecture
- [docs/cli.md](docs/cli.md) — Exhaustive CLI command reference
- [docs/providers.md](docs/providers.md) — Provider adapter specifications and configuration
- [docs/mcp.md](docs/mcp.md) — Model Context Protocol (2026-07-28) setup & Bearer auth
- [docs/a2a.md](docs/a2a.md) — Agent-to-Agent (1.0) wire protocol guide
- [docs/policy-as-code.md](docs/policy-as-code.md) — Policy engine rules and capability broker
- [docs/troubleshooting.md](docs/troubleshooting.md) — Diagnostics and recovery guide

---

## Community & Contributing

We welcome community contributions! Please review our guidelines before submitting pull requests:
- [CONTRIBUTING.md](CONTRIBUTING.md) — Code formatting, testing requirements, and submission process
- [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) — Community standards and expectations
- [SECURITY.md](SECURITY.md) — Vulnerability reporting policy (private security contact)

---

## Licensing

MARSHAL is made available under dual-licensing terms:

* **Community Edition**: Licensed under the GNU Affero General Public License v3.0 only (`AGPL-3.0-only`). See [LICENSE](LICENSE).
* **Commercial Licensing**: Alternative commercial licenses are available for enterprise organizations requiring non-AGPL terms. See [LICENSING.md](LICENSING.md) and [COMMERCIAL-LICENSING.md](COMMERCIAL-LICENSING.md).
* **Historical Grants**: Historical releases up to `runtime-v0.4.0` remain available under their original `Apache-2.0` grants. See [docs/legal/LICENSE-HISTORY.md](docs/legal/LICENSE-HISTORY.md).
