# SLAVES CLI Reference

**Runtime Version**: `v0.4.0`

This document provides a comprehensive command reference for the `slaves` command-line executable.

---

## Global Options

```text
Usage: slaves [--json] <command> [arguments]
```

| Option | Description |
|---|---|
| `--json` | Format output as structured JSON instead of human-readable text |

---

## Core Operational Commands

### `slaves init`

Purpose: Initializes the private `.slaves/` runtime state directory inside the current Git repository. Safe and idempotent.

```bash
slaves init
```

Output:
```text
initialized /path/to/repo/.slaves
```

---

### `slaves doctor`

Purpose: Runs system health diagnostics, checking prerequisites, Git worktree capability, database integrity, file permissions, and provider binaries.

```bash
# Standard diagnostic check
slaves doctor

# Deep provider capability probing
slaves doctor --probe-providers
```

Flags:
- `--probe-providers`: Perform execution probing against installed LLM provider binaries.

---

### `slaves status`

Purpose: Connects to the local daemon socket and displays active runtime status, database schema version, active tasks count, and registered agents count.

```bash
slaves status
```

Output:
```text
schema=2 tasks=1 agents=1
```

---

### `slaves daemon`

Purpose: Starts the local SLAVES control plane daemon process in the foreground. Listens on Unix socket `.slaves/runtime.sock`. Automatically cleans up dead PID files on startup.

```bash
slaves daemon
```

---

## Agent Management

### `slaves agent register`

Purpose: Registers a new agent principal in the SQLite database with an assigned engineering role.

```bash
slaves agent register --name <NAME> --role <ROLE>
```

Flags:
- `--name`: Human-readable name for the agent (e.g. `OperatorAgent`)
- `--role`: Assigned role (`architect`, `developer`, `qa`, `security`)

Example:
```bash
slaves agent register --name CodexDeveloper --role developer
```

---

### `slaves agents`

Purpose: Lists all registered agents in the workspace.

```bash
slaves agents
```

---

## Task Management

### `slaves task import`

Purpose: Imports task definitions from a JSON file into the control plane SQLite database.

```bash
slaves task import <FILE.json> [--dry-run]
```

Flags:
- `--dry-run`: Validate task schema without committing to SQLite

---

### `slaves tasks`

Purpose: Displays all tasks currently tracked in the workspace database.

```bash
slaves tasks
```

---

### `slaves task show`

Purpose: Shows detailed state, revision, lease status, and branch metadata for a single task.

```bash
slaves task show <TASK-ID>
```

---

### `slaves task claim`

Purpose: Claims a task lease for a registered agent principal.

```bash
slaves task claim <TASK-ID> --agent <AGENT-ID> [--revision <N>]
```

---

### `slaves task release`

Purpose: Releases an active task lease.

```bash
slaves task release <TASK-ID>
```

---

## Execution Commands

### `slaves run`

Purpose: Executes a ready task using a specified provider adapter and sandbox environment.

```bash
slaves run <TASK-ID> --adapter <ADAPTER> [--model <MODEL>] [--agent <AGENT-ID>]
```

Flags:
- `--adapter`: Provider adapter name (`codex`, `opencode`, `gemini`, `claude`)
- `--model`: Optional model override (e.g. `qwythos-9b` for Ollama)
- `--agent`: Optional agent ID claiming execution

Example:
```bash
slaves run TASK-001 --adapter codex
slaves run TASK-001 --adapter opencode --model qwythos-9b
```

---

### `slaves logs`

Purpose: Displays stdout/stderr execution logs, generated artifacts, and timeline events for a task.

```bash
slaves logs <TASK-ID>
```

---

### `slaves cancel`

Purpose: Gracefully cancels an active task execution.

```bash
slaves cancel <TASK-ID>
```

---

## Provider & Adapter Commands

### `slaves adapters`

Purpose: Displays all registered provider adapters, discovered binary paths, and availability status.

```bash
slaves adapters
```

Sample Output:
```text
=== SLAVES Provider Adapters ===
  codex      AVAILABLE    binary=/home/user/.local/bin/codex   version=codex-cli 0.146.0
  gemini     AVAILABLE    binary=/usr/bin/gemini               version=0.50.0
  claude     AVAILABLE    binary=/home/user/.local/bin/claude  version=2.1.218 (Claude Code)
  opencode   AVAILABLE    binary=/home/user/.local/bin/opencode version=1.18.16
```

---

### `slaves adapter probe`

Purpose: Probes a specific provider adapter by name to test flags and binary responses.

```bash
slaves adapter probe <NAME>
```

---

## Authentication & Tokens

### `slaves auth token create`

Purpose: Generates a high-entropy Bearer authentication token for MCP or A2A clients.

```bash
slaves auth token create --name <NAME>
```

Output:
```text
Created Token ID: TOKEN-e6eeb825c43740c7
Plaintext Token: slaves_token_6e86f061e255da6d5b075084e...
(Keep this token secret; it will not be shown again)
```

---

### `slaves auth token list`

Purpose: Lists all active and revoked Bearer tokens.

```bash
slaves auth token list
```

---

### `slaves auth token revoke`

Purpose: Revokes a Bearer authentication token by Token ID.

```bash
slaves auth token revoke --id <TOKEN-ID>
```

---

## Protocol Server Commands

### `slaves mcp serve` / `slaves mcp status`

Purpose: Runs the Model Context Protocol (MCP 2026-07-28) server endpoint or checks server status.

```bash
# Start MCP HTTP server
slaves mcp serve [--listen ADDR]

# Check MCP status
slaves mcp status
```

---

### `slaves a2a serve` / `slaves a2a status`

Purpose: Runs the Agent-to-Agent (A2A 1.0) protocol server endpoint or checks server status.

```bash
# Start A2A HTTP server
slaves a2a serve [--listen ADDR]

# Check A2A status
slaves a2a status
```

---

## Events, Verification & Audit Commands

### `slaves events`

Purpose: Displays the chronological audit log of workspace events.

```bash
slaves events
```

---

### `slaves artifacts`

Purpose: Lists all execution artifacts stored in `.slaves/artifacts`.

```bash
slaves artifacts
```

---

### `slaves verify`

Purpose: Runs repository verification commands against current code state.

```bash
slaves verify [-- command args...]
```

---

### `slaves reconcile`

Purpose: Reconciles workspace task state against file state.

```bash
slaves reconcile --file-state state.json
```
