# Getting Started with MARSHAL

**Vibe coding, without Vulnerability-as-a-Service.**

This guide walks a new engineer through installing, initializing, configuring, and executing AI coding agent tasks with MARSHAL.

---

## Prerequisites

Before starting, ensure your host environment meets the following requirements:

| Component | Minimum Version | Required For |
|---|---|---|
| **OS** | Linux (Ubuntu, Debian, Fedora, Arch, etc.) | Process sandboxing (`bwrap`) & Git worktrees |
| **Git** | 2.30+ | Task branch creation and worktree isolation |
| **Go** | 1.25+ | Building and running the `marshal` local control plane |
| **Python** | 3.10+ | Static pack validation and conformance testing |
| **bubblewrap** | `bwrap` binary installed | Fail-closed filesystem & namespace isolation |
| **Codex CLI** | `codex-cli 0.146.0`+ | Executing tasks using the `codex` adapter |
| **OpenCode** | `1.18.16`+ | Executing tasks using local LLM models |
| **Ollama** | `0.32.6`+ (`qwythos-9b` model) | Local model provider backend for OpenCode |

---

## 1. Installation

From your local clone of the `marshal` repository:

```bash
# Clone the repository
git clone https://github.com/Zen1th53/marshal.git
cd marshal

# Install the marshal binary to $GOPATH/bin
go install ./cmd/marshal
```

Verify that `marshal` is in your `$PATH`:
```bash
marshal --help
```

---

## 2. Workspace Initialization & Diagnostics

`marshal init` initializes the `.marshal/` runtime state directory inside your current Git repository. It is idempotent and safe to re-run.

```bash
# Step 1: Initialize local workspace state
marshal init

# Step 2: Run health diagnostics
marshal doctor
```

Expected `marshal doctor` output:
```text
MARSHAL Doctor Verdict: PASS
========================================
[PASS]     git              Git is available
[PASS]     repository       repository identity resolved
[PASS]     pack             pack version 6.0.0
[PASS]     runtime_version  runtime specification 1.0.0
[PASS]     sqlite           schema version 2 is healthy
[PASS]     permissions      runtime directory mode is 0700
[PASS]     socket           daemon is not running
[PASS]     worktree         Git worktrees are supported
[PASS]     codex            codex-cli 0.146.0
[PASS]     opencode         1.18.16
[PASS]     ollama           ollama version is 0.32.6 (http://localhost:11434)
[PASS]     gemini           0.50.0
[PASS]     claude           2.1.218 (Claude Code)
[PASS]     bwrap            strong local isolation is available
[PASS]     artifacts        artifact directory is writable and mode 0700
[PASS]     policy           policy is available
```

To view installed provider binaries:
```bash
marshal adapters
```

---

## 3. Starting the Daemon Process

MARSHAL runs a local control plane daemon process that manages transactional task leases, SQLite database connections, and worker execution.

Start the daemon in a background terminal:
```bash
marshal daemon
```

In a separate terminal, verify the daemon connection:
```bash
marshal status
```

Output:
```text
schema=2 tasks=0 agents=0
```

---

## 4. Agent Registration & Task Import

### Register an Agent
Agents must be registered with an explicit role (`architect`, `developer`, `qa`, `security`) before claiming or executing tasks:

```bash
marshal agent register --name OperatorAgent --role developer
```

Response:
```text
AGENT-6901a5e65c4b30a3049d256623d06c21
```

List registered agents:
```bash
marshal agents
```

### Import Tasks
Create a `tasks.json` file in your repository:
```json
[
  {
    "id": "TASK-001",
    "title": "Create application metadata file",
    "status": "ready",
    "risk": "R1",
    "base_commit": "HEAD",
    "head_commit": "HEAD"
  }
]
```

Validate and import the task:
```bash
# Dry run validation
marshal task import tasks.json --dry-run

# Import into SQLite state store
marshal task import tasks.json

# List tasks
marshal tasks

# Show task details
marshal task show TASK-001
```

---

## 5. Executing Tasks

### Execute Task with Codex
To execute the task using the Codex adapter:

```bash
marshal run TASK-001 --adapter codex
```

During execution, MARSHAL:
1. Creates an isolated Git worktree at `.marshal/worktrees/TASK-001`.
2. Spawns `codex` inside a `bubblewrap` sandbox.
3. Captures stdout/stderr execution logs and events into SQLite.
4. Commits code changes to branch `marshal/TASK-001`.

### Execute Task with OpenCode + Local Ollama
To execute the task locally using OpenCode and an Ollama model (e.g. `qwythos-9b`):

```bash
marshal run TASK-001 --adapter opencode --model qwythos-9b
```

---

## 6. Daily Monitoring, Logs & Cancellation

### Inspect Task Logs and Events
To view execution logs, stdout/stderr artifacts, and event history for a task:

```bash
marshal logs TASK-001
```

Sample output:
```text
=== Logs & Events for TASK-001 ===
Events (2):
  [2026-08-12T10:24:01Z] TASK_CLAIMED
  [2026-08-12T10:24:01Z] TASK_RELEASED
Artifacts (1):
  [2026-08-12T10:24:01Z] stdout (842 bytes)
```

### Cancel an Active Task
To cancel a running task execution gracefully:

```bash
marshal cancel TASK-001
```

---

## 7. Generating Interoperability Auth Tokens

For remote agents or MCP / A2A clients connecting to MARSHAL:

```bash
# Generate a Bearer token
marshal auth token create --name mcp-operator

# List active tokens
marshal auth token list

# Revoke a token
marshal auth token revoke --id TOKEN-ID
```

---

## Next Steps

- Read the [CLI Reference](cli.md) for full command flags.
- Explore [Provider Setup](providers.md) and [OpenCode + Ollama Setup](providers/opencode-ollama.md).
- Learn about [MCP Integration](mcp.md) and [A2A Integration](a2a.md).
- Consult [Troubleshooting](troubleshooting.md) for common error resolution.
