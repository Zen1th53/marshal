# MARSHAL CLI Reference

**Runtime Version**: `v1.0.0`

This document provides a comprehensive command reference for the `marshal` command-line executable.

---

## Global Options

```text
Usage: marshal [--json] <command> [arguments]
```

| Option | Description |
|---|---|
| `--json` | Format output as structured JSON instead of human-readable text |

---

## Core Operational Commands

### `marshal init`

Purpose: Initializes the private `.marshal/` runtime state directory inside the current Git repository. Safe and idempotent.

```bash
marshal init
```

Output:
```text
initialized /path/to/repo/.marshal
```

---

### `marshal doctor`

Purpose: Runs system health diagnostics, checking prerequisites, Git worktree capability, database integrity, file permissions, and provider binaries.

```bash
# Standard diagnostic check
marshal doctor

# Deep provider capability probing
marshal doctor --probe-providers
```

Flags:
- `--probe-providers`: Perform execution probing against installed LLM provider binaries.

---

### `marshal status`

Purpose: Connects to the local daemon socket and displays active runtime status, database schema version, active tasks count, and registered agents count.

```bash
marshal status
```

Output:
```text
schema=2 tasks=1 agents=1
```

---

### `marshal daemon`

Purpose: Starts the local MARSHAL control plane daemon process in the foreground. Listens on Unix socket `.marshal/runtime.sock`. Automatically cleans up dead PID files on startup.

```bash
marshal daemon
```

---

## Agent Management

### `marshal agent register`

Purpose: Registers a new agent principal in the SQLite database with an assigned engineering role.

```bash
marshal agent register --name <NAME> --role <ROLE>
```

Flags:
- `--name`: Human-readable name for the agent (e.g. `OperatorAgent`)
- `--role`: Assigned role (`architect`, `developer`, `qa`, `security`)

Example:
```bash
marshal agent register --name CodexDeveloper --role developer
```

---

### `marshal agents`

Purpose: Lists all registered agents in the workspace.

```bash
marshal agents
```

---

## Task Management

### `marshal task import`

Purpose: Imports task definitions from a JSON file into the control plane SQLite database.

```bash
marshal task import <FILE.json> [--dry-run]
```

Flags:
- `--dry-run`: Validate task schema without committing to SQLite

---

### `marshal tasks`

Purpose: Displays all tasks currently tracked in the workspace database.

```bash
marshal tasks
```

---

### `marshal task show`

Purpose: Shows detailed state, revision, lease status, and branch metadata for a single task.

```bash
marshal task show <TASK-ID>
```

---

### `marshal task claim`

Purpose: Claims a task lease for a registered agent principal.

```bash
marshal task claim <TASK-ID> --agent <AGENT-ID> [--revision <N>]
```

---

### `marshal task release`

Purpose: Releases an active task lease.

```bash
marshal task release <TASK-ID>
```

---

## Execution Commands

### `marshal policy test`

Evaluates a declarative T49 JSON policy-test suite without activating or
mutating a policy-test lifecycle run. The suite is strictly decoded and bound
to the exact policy digest supplied by every case.

```bash
marshal policy test policy-suite.json
marshal --json policy test policy-suite.json
```

`PASS` exits `0`. A failed case, evaluator error, malformed/unknown-field
input, or unavailable file exits non-zero. Use `--json` for automation; the
typed `status`, `policy_digest`, case status, reason, and stable diff are the
source of truth rather than human output parsing. Raw fixtures, evaluator
output, and backend error text are not printed.

### `marshal run`

Purpose: Executes a ready task using a specified provider adapter and sandbox environment.

```bash
marshal run <TASK-ID> --adapter <ADAPTER> [--model <MODEL>] [--agent <AGENT-ID>]
```

Flags:
- `--adapter`: Provider adapter name (`codex`, `opencode`, `gemini`, `claude`)
- `--model`: Optional model override (e.g. `qwythos-9b` for Ollama)
- `--agent`: Optional agent ID claiming execution

Example:
```bash
marshal run TASK-001 --adapter codex
marshal run TASK-001 --adapter opencode --model qwythos-9b
```

---

### `marshal logs`

Purpose: Displays stdout/stderr execution logs, generated artifacts, and timeline events for a task.

```bash
marshal logs <TASK-ID>
```

---

### `marshal cancel`

Purpose: Gracefully cancels an active task execution.

```bash
marshal cancel <TASK-ID>
```

---

## Provider & Adapter Commands

### `marshal adapters`

Purpose: Displays all registered provider adapters, discovered binary paths, and availability status.

```bash
marshal adapters
```

Sample Output:
```text
=== MARSHAL Provider Adapters ===
  codex      AVAILABLE    binary=/home/user/.local/bin/codex   version=codex-cli 0.146.0
  gemini     AVAILABLE    binary=/usr/bin/gemini               version=0.50.0
  claude     AVAILABLE    binary=/home/user/.local/bin/claude  version=2.1.218 (Claude Code)
  opencode   AVAILABLE    binary=/home/user/.local/bin/opencode version=1.18.16
```

---

### `marshal adapter probe`

Purpose: Probes a specific provider adapter by name to test flags and binary responses.

```bash
marshal adapter probe <NAME>
```

---

## Authentication & Tokens

### `marshal auth token create`

Purpose: Generates a high-entropy Bearer authentication token for MCP or A2A clients.

```bash
marshal auth token create --name <NAME>
```

Output:
```text
Created Token ID: TOKEN-e6eeb825c43740c7
Plaintext Token: marshal_token_6e86f061e255da6d5b075084e...
(Keep this token secret; it will not be shown again)
```

---

### `marshal auth token list`

Purpose: Lists all active and revoked Bearer tokens.

```bash
marshal auth token list
```

---

### `marshal auth token revoke`

Purpose: Revokes a Bearer authentication token by Token ID.

```bash
marshal auth token revoke --id <TOKEN-ID>
```

---

## Protocol Server Commands

### `marshal mcp serve` / `marshal mcp status`

Purpose: Runs the Model Context Protocol (MCP 2026-07-28) server endpoint or checks server status.

```bash
# Start MCP HTTP server
marshal mcp serve [--listen ADDR]

# Check MCP status
marshal mcp status
```

---

### `marshal a2a serve` / `marshal a2a status`

Purpose: Runs the Agent-to-Agent (A2A 1.0) protocol server endpoint or checks server status.

```bash
# Start A2A HTTP server
marshal a2a serve [--listen ADDR]

# Check A2A status
marshal a2a status
```

---

## Events, Verification & Audit Commands

### `marshal events`

Purpose: Displays the chronological audit log of workspace events.

```bash
marshal events
```

---

### `marshal artifacts`

Purpose: Lists all execution artifacts stored in `.marshal/artifacts`.

```bash
marshal artifacts
```

---

### `marshal verify`

Purpose: Runs repository verification commands against current code state.

```bash
marshal verify [-- command args...]
```

---

### `marshal reconcile`

Purpose: Reconciles workspace task state against file state.

```bash
marshal reconcile --file-state state.json
```
