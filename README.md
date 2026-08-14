# MARSHAL

**Vibe coding, without Vulnerability-as-a-Service.**

AI agents can generate code at extreme speed.

Security failures can scale at exactly the same speed.

MARSHAL provides the execution boundary between an AI coding agent and a real
software project: isolating work, enforcing policy, controlling permissions,
requiring evidence, verifying results, and maintaining an auditable trail of
what happened.

Fast agents.

Controlled execution.

Verified results.

**Move fast. Grant less. Verify everything.**

[![License](https://img.shields.io/badge/license-AGPL--3.0--only-blue.svg)](LICENSING.md)
[![CI](https://github.com/Zen1th53/marshal/actions/workflows/ci.yml/badge.svg)](https://github.com/Zen1th53/marshal/actions/workflows/ci.yml)

---

## What MARSHAL Is

MARSHAL is a security-first runtime and control plane for agentic software
engineering. It turns unconstrained AI coding into governed execution through
isolation, policy enforcement, scoped permissions, approvals, deterministic
verification, evidence collection, reconciliation, and auditability.

Core engineering roles—**Orchestrator**, **Architect**, **Developer**, **QA**,
and **AppSec**—have explicit ownership and authority boundaries. Vendor-neutral
process adapters connect agents to the runtime without granting any model
unlimited trust.

Agents can move fast.

They do not get unlimited trust.

---

## Why MARSHAL Exists

Autonomous AI coding agents operating without control plane constraints risk making unverified code changes, operating on stale context, approving their own work, missing security policies, leaking secrets, or failing to produce audit evidence.

MARSHAL enforces explicit contracts:

- **Least privilege by default**: Agents receive scoped permissions for owned tasks, not blanket access.
- **Isolated execution**: Work runs in task-specific Git worktrees and fail-closed Linux sandboxes.
- **Policy and approval gates**: Risky operations require explicit authorization before execution.
- **Repository evidence > memory**: Code changes must compile, pass verification, and produce git commits.
- **One active task = one owner**: Strict transactional task leases prevent concurrent file overwrites.
- **No PASS without evidence**: Verification requires empirical test results and clean git diffs.
- **Auditable and reproducible execution**: Events, artifacts, decisions, and verification bind back to source state.

---

## Current Release Status

| Property | Value |
|---|---|
| **Source Channel** | `main` |
| **Pack Version** | `6.0.0` |
| **MCP Protocol** | `2026-07-28` |
| **A2A Wire Version** | `1.0` |
| **Platform** | Linux-first / Local-first |
| **Sandbox Engine** | `bubblewrap` (bwrap) |
| **State Storage** | SQLite (schema version 2) |

---

## Provider Support Matrix

MARSHAL probes provider binaries dynamically and tracks provider maturity across six distinct states: `IMPLEMENTED`, `INSTALLED`, `AVAILABLE`, `AUTHENTICATED`, `CAPABILITY-PROBED`, and `REAL-E2E-VERIFIED`.

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
git clone https://github.com/Zen1th53/marshal.git
cd marshal

go install ./cmd/marshal
```

### 2. Initialize and Run Doctor
```bash
# Initialize .marshal state directory in your repository
marshal init

# Run system health diagnostics
marshal doctor
```

### 3. Start Local Daemon
Start the daemon process in a separate background terminal or service:
```bash
marshal daemon
```

Verify daemon health:
```bash
marshal status
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
marshal agent register --name OperatorAgent --role developer

# Import tasks into SQLite control plane
marshal task import tasks.json
marshal tasks
```

### Option A: Execute Task with Codex
```bash
marshal run TASK-DEMO-001 --adapter codex
```

### Option B: Execute Task with OpenCode + Local Ollama
Requires `ollama` running locally with a tool-capable model (e.g. `qwythos-9b`):
```bash
marshal run TASK-DEMO-001 --adapter opencode --model qwythos-9b
```

### Inspect Logs and Status
```bash
# View task execution logs, artifacts, and timeline events
marshal logs TASK-DEMO-001

# Inspect runtime state
marshal status
```

### Cancel Task Execution
```bash
marshal cancel TASK-DEMO-001
```

---

## Daily Operator Commands

| Command | Purpose |
|---|---|
| `marshal doctor [--probe-providers]` | Run system health diagnostics and optional provider probes |
| `marshal adapters` | Display discovered provider binaries and availability status |
| `marshal adapter probe <NAME>` | Perform deep capability probe for a specific adapter |
| `marshal status` | Show active tasks, registered agents, and daemon status |
| `marshal run <TASK-ID> --adapter <NAME> [--model <MODEL>]` | Execute a ready task with a specific provider adapter |
| `marshal logs <TASK-ID>` | Display stdout/stderr logs, artifacts, and event timeline |
| `marshal cancel <TASK-ID>` | Cancel an active task execution gracefully |
| `marshal auth token create --name <NAME>` | Generate Bearer token for authenticated MCP/A2A protocols |

---

## Architecture Overview

```text
                     USER / ORCHESTRATOR
                              │
                     CLI / MCP / A2A
                              │
                    MARSHAL Runtime Server
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
- **Authenticated Interoperability**: MCP and A2A endpoints require high-entropy Bearer tokens generated via `marshal auth token create`.
- **Secrets Redaction**: Automatic boundary redaction prevents sensitive environment keys from appearing in events or logs.

Read our full [Security Policy](SECURITY.md) and [Security Model](docs/security-model.md).

---

## Documentation Index

- [Getting Started Guide](docs/getting-started.md) — Comprehensive tutorial from zero to first task
- [CLI Reference](docs/cli.md) — Complete usage for all `marshal` commands
- [Provider Guide](docs/providers.md) — Capability model and provider setup
- [OpenCode + Ollama Setup](docs/providers/opencode-ollama.md) — Local LLM task execution guide
- [MCP Protocol Guide](docs/mcp.md) — MCP 2026-07-28 integration & Bearer auth
- [A2A Protocol Guide](docs/a2a.md) — Agent-to-Agent 1.0 integration guide
- [Troubleshooting Guide](docs/troubleshooting.md) — Symptom-based diagnostics & recovery
- [Architecture Guide](docs/architecture.md) — Reader guide & contract boundary
- [Contributing Guide](CONTRIBUTING.md) — Guidelines for code and doc contributions

---

## License

MARSHAL uses a dual-licensing model:

* **Community Edition**: Licensed under the GNU Affero General Public License v3.0 only (`AGPL-3.0-only`). See [LICENSE](LICENSE).
* **Commercial License**: Alternative commercial licensing is available for organizations requiring non-AGPL terms. See [LICENSING.md](LICENSING.md) and [COMMERCIAL-LICENSING.md](COMMERCIAL-LICENSING.md).
* **Contributions**: Material external contributions require completion of the MARSHAL contributor assignment process before merge. See [CONTRIBUTING.md](CONTRIBUTING.md).
* **Historical Releases**: Historical releases up to `runtime-v0.4.0` remain available under their original `Apache-2.0` grants. See [docs/legal/LICENSE-HISTORY.md](docs/legal/LICENSE-HISTORY.md).
