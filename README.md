# SLAVES

**Structured Lifecycle for Agent Verification, Execution & Supervision**

A vendor-neutral engineering control plane for disciplined multi-agent AI software development.

[![License](https://img.shields.io/badge/license-AGPL--3.0--only-blue.svg)](LICENSING.md)
[![CI](https://github.com/Zen1th53/slaves/actions/workflows/ci.yml/badge.svg)](https://github.com/Zen1th53/slaves/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/Zen1th53/slaves?sort=semver)](https://github.com/Zen1th53/slaves/releases)

---

## What SLAVES Is

SLAVES provides AI coding agents with a structured, local-first control plane governing task allocation, architecture decisions, code implementation, QA, AppSec, memory, policy enforcement, process sandboxing, and artifact provenance.

Core engineering roles—**Orchestrator**, **Architect**, **Developer**, **QA**, and **AppSec**—are explicitly defined. Agent vendors connect to SLAVES through process adapters; no single AI model or vendor acts as the architectural source of authority.

---

## Why SLAVES Exists

Autonomous AI coding agents operating without control plane constraints risk making unverified code changes, operating on stale context, approving their own work, missing security policies, leaking secrets, or failing to produce audit evidence.

SLAVES enforces explicit contracts:
- **Repository evidence > memory**: Code changes must compile, pass verification, and produce git commits.
- **One active task = one owner**: Strict transactional task leases prevent concurrent file overwrites.
- **Fail-closed sandboxing**: Code execution occurs inside isolated Git worktrees and Linux `bubblewrap` sandboxes.
- **No PASS without evidence**: Verification requires empirical test results and clean git diffs.

---

## Current Release Status

| Property | Value |
|---|---|
| **Stable Release** | [`runtime-v0.4.0`](https://github.com/Zen1th53/slaves/releases/tag/runtime-v0.4.0) |
| **Pack Version** | `6.0.0` |
| **MCP Protocol** | `2026-07-28` |
| **A2A Wire Version** | `1.0` |
| **Platform** | Linux-first / Local-first |
| **Sandbox Engine** | `bubblewrap` (bwrap) |
| **State Storage** | SQLite (schema version 2) |

---

## Provider Support Matrix

SLAVES probes provider binaries dynamically and tracks provider maturity across six distinct states: `IMPLEMENTED`, `INSTALLED`, `AVAILABLE`, `AUTHENTICATED`, `CAPABILITY-PROBED`, and `REAL-E2E-VERIFIED`.

| Provider Adapter | Version / Binary | Verification Status | Native | MCP | A2A |
|---|---|---|:---:|:---:|:---:|
| **Codex** | `codex-cli` | **REAL E2E VERIFIED** | ✅ | ✅ | ✅ |
| **OpenCode + Local Ollama** | `opencode` + `qwythos-9b` | **REAL E2E VERIFIED** | ✅ | ✅ | ✅ |
| **Gemini CLI** | `gemini` | **PROBED / UNVERIFIED** *(API Quota Limited)* | — | — | — |
| **Claude Code** | `claude` | **PROBED / UNVERIFIED** *(OAuth Session Expired)* | — | — | — |
| **Aider** | `aider` | Defined *(Contract Specification)* | — | — | — |
| **Crush** | `crush` | Defined *(Contract Specification)* | — | — | — |

---

## 5-Minute Quick Start

### Prerequisites
- **OS**: Linux host (Ubuntu, Debian, Fedora, Arch, etc.)
- **Dependencies**: Git, Go `1.25`+, Linux `bubblewrap` (`bwrap`)
- **Providers**: `codex-cli` (for Codex) or `opencode` + `ollama` (for local models)

### 1. Build and Install
```bash
git clone https://github.com/Zen1th53/slaves.git
cd slaves
git checkout runtime-v0.4.0

go install ./cmd/slaves
```

### 2. Initialize and Run Doctor
```bash
# Initialize .slaves state directory in your repository
slaves init

# Run system health diagnostics
slaves doctor
```

### 3. Start Local Daemon
Start the daemon process in a separate background terminal or service:
```bash
slaves daemon
```

Verify daemon health:
```bash
slaves status
```

---

## First Task Workflow

### Task Definition Schema
Save the following task definition to `tasks.json`:
```json
[
  {
    "id": "TASK-DEMO-001",
    "title": "Create application status file",
    "status": "ready",
    "risk": "R1",
    "base_commit": "HEAD",
    "head_commit": "HEAD"
  }
]
```

Import and register your agent:
```bash
# Register an agent
slaves agent register --name OperatorAgent --role developer

# Import tasks into SQLite control plane
slaves task import tasks.json
slaves tasks
```

### Option A: Execute Task with Codex
```bash
slaves run TASK-DEMO-001 --adapter codex
```

### Option B: Execute Task with OpenCode + Local Ollama
Requires `ollama` running locally with a tool-capable model (e.g. `qwythos-9b`):
```bash
slaves run TASK-DEMO-001 --adapter opencode --model qwythos-9b
```

### Inspect Logs and Status
```bash
# View task execution logs, artifacts, and timeline events
slaves logs TASK-DEMO-001

# Inspect runtime state
slaves status
```

### Cancel Task Execution
```bash
slaves cancel TASK-DEMO-001
```

---

## Daily Operator Commands

| Command | Purpose |
|---|---|
| `slaves doctor [--probe-providers]` | Run system health diagnostics and optional provider probes |
| `slaves adapters` | Display discovered provider binaries and availability status |
| `slaves adapter probe <NAME>` | Perform deep capability probe for a specific adapter |
| `slaves status` | Show active tasks, registered agents, and daemon status |
| `slaves run <TASK-ID> --adapter <NAME> [--model <MODEL>]` | Execute a ready task with a specific provider adapter |
| `slaves logs <TASK-ID>` | Display stdout/stderr logs, artifacts, and event timeline |
| `slaves cancel <TASK-ID>` | Cancel an active task execution gracefully |
| `slaves auth token create --name <NAME>` | Generate Bearer token for authenticated MCP/A2A protocols |

---

## Architecture Overview

```text
                     USER / ORCHESTRATOR
                              │
                     CLI / MCP / A2A
                              │
                    SLAVES Runtime Server
                              │
              ┌───────────────┼───────────────┐
              │               │               │
            TASKS          MEMORY           POLICY
          (SQLite)        (Events)       (Capabilities)
              │               │               │
              └───────────────┼───────────────┘
                              │
                    Process Supervisor
                              │
                 Git Worktree + Bubblewrap
                              │
                    Provider Adapter
              ┌───────────────┴───────────────┐
              │                               │
         Codex Adapter                OpenCode Adapter
              │                               │
          Codex CLI                   OpenCode → Ollama
                              │
                 Artifacts & Evidence Store
```

For detailed architectural semantics, read [Architecture Guide](docs/architecture.md) and [Runtime Architecture](runtime/ARCHITECTURE.md).

---

## Security Positioning

- **Local-First & Socket-Protected**: Daemon listens exclusively on permission-restricted Unix sockets (`0600`) or authenticated localhost endpoints.
- **Fail-Closed Sandboxing**: Unprivileged bubblewrap namespaces prevent host filesystem tampering.
- **Authenticated Interoperability**: MCP and A2A endpoints require high-entropy Bearer tokens generated via `slaves auth token create`.
- **Secrets Redaction**: Automatic boundary redaction prevents sensitive environment keys from appearing in events or logs.

Read our full [Security Policy](SECURITY.md) and [Security Model](docs/security-model.md).

---

## Documentation Index

- [Getting Started Guide](docs/getting-started.md) — Comprehensive tutorial from zero to first task
- [CLI Reference](docs/cli.md) — Complete usage for all `slaves` commands
- [Provider Guide](docs/providers.md) — Capability model and provider setup
- [OpenCode + Ollama Setup](docs/providers/opencode-ollama.md) — Local LLM task execution guide
- [MCP Protocol Guide](docs/mcp.md) — MCP 2026-07-28 integration & Bearer auth
- [A2A Protocol Guide](docs/a2a.md) — Agent-to-Agent 1.0 integration guide
- [Troubleshooting Guide](docs/troubleshooting.md) — Symptom-based diagnostics & recovery
- [Architecture Guide](docs/architecture.md) — Reader guide & contract boundary
- [Contributing Guide](CONTRIBUTING.md) — Guidelines for code and doc contributions

---

## License

SLAVES uses a dual-licensing model:

* **Community Edition**: Licensed under the GNU Affero General Public License v3.0 only (`AGPL-3.0-only`). See [LICENSE](LICENSE).
* **Commercial License**: Alternative commercial licensing is available for organizations requiring non-AGPL terms. See [LICENSING.md](LICENSING.md) and [COMMERCIAL-LICENSING.md](COMMERCIAL-LICENSING.md).
* **Contributions**: Material external contributions require completion of the SLAVES contributor assignment process before merge. See [CONTRIBUTING.md](CONTRIBUTING.md).
* **Historical Releases**: Historical releases up to `runtime-v0.4.0` remain available under their original `Apache-2.0` grants. See [docs/legal/LICENSE-HISTORY.md](docs/legal/LICENSE-HISTORY.md).
