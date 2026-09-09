# MARSHAL CLI Reference

**Runtime Version**: `v1.0.1`

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

Purpose: Creates missing project policy/version defaults and initializes the private `.marshal/` runtime state directory inside the current Git repository. Existing regular defaults are preserved; symlinks in their place are rejected.

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
schema=72 tasks=1 agents=1
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

## Governed Lifecycle Commands

These drive the Process 03–08 lifecycle merged on `main`. Each stage writes a
durable, versioned record bound to an exact repository state. Every command here
reads canonical state or asks the runtime service to act; none can mint a
success state directly.

### `marshal goal`

Purpose: States a request and shows how MARSHAL understands it — intent, hard
constraints and risk tier — before any plan or execution exists.

```bash
marshal goal <request>
marshal goal explain <request>
```

---

### `marshal plan`

Purpose: Creates, inspects and approves a task plan: its DAG, team assembly and
verification policy.

```bash
marshal plan create SESSION-ID --file INPUT.json
marshal plan show PROJECT-ID
marshal plan approve PROJECT-ID
marshal plan cancel PROJECT-ID
marshal plan handoff SESSION-ID PROJECT-ID
```

---

### `marshal exec`

Purpose: Drives a governed execution run, approves a pending gate, or rolls back
to a checkpoint.

```bash
marshal exec start --session SESSION-ID --project PROJECT-ID
marshal exec run RUN-ID
marshal exec status RUN-ID
marshal exec approve APPROVAL-ID
marshal exec rollback CHECKPOINT-ID
marshal exec handoff RUN-ID
```

---

### `marshal review`

Purpose: Runs independent verification and issues a digest-bound completion
attestation. A run that exited zero is not a verified run; completion requires
every mandatory criterion met and critical evidence from independent sources.

```bash
marshal review start SESSION.json
marshal review status VERIFICATION-ID
marshal review evaluate VERIFICATION-ID
marshal review attest VERIFICATION-ID --bundle ENVELOPE.json --provenance TEXT
```

---

### `marshal learning`

Purpose: Queries evidence-gated durable memory. Results carry their claim state,
freshness and contradiction signals, so a stale or contested claim is returned
marked unusable rather than silently omitted.

```bash
marshal learning search --project ID [--general] [--terms A,B] [--stale]
marshal learning show MEMORY-COMMIT-ID
marshal learning history ITEM-ID
marshal learning trust [TASK-CLASS]
marshal learning fingerprints --project ID
marshal learning playbooks --project ID
marshal learning export --project ID
```

Notes:
- `trust` reports measured routing outcomes including failures, blocked runs and
  routes that were never selected, alongside a selection-bias flag. An unmeasured
  cost prints as `unmeasured`, never as zero.
- `playbooks` lists candidates only. A playbook never self-activates.

---

### `marshal optimization`

Purpose: Inspects optimization cycles, counterfactual route evaluations,
benchmark manifests and bounded canaries.

```bash
marshal optimization start INPUT.json
marshal optimization show CYCLE-ID
marshal optimization candidates CYCLE-ID
marshal optimization counterfactuals CYCLE-ID
marshal optimization manifests CYCLE-ID
marshal optimization canaries CYCLE-ID
```

Notes:
- A counterfactual is refused where the original run performed a destructive
  external effect, because re-running it would repeat that effect.
- Promotion, canary and rollback are runtime-service operations. This surface is
  read-only.

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

---

## Interactive Terminal Command Center

### `marshal tui`

Purpose: Launches the MARSHAL TUI v2 interactive multi-agent command center and control plane. When executed in an interactive terminal, running `marshal` without arguments also automatically opens the TUI.

```bash
# Launch interactive TUI
marshal tui

# Launch with specific options
marshal tui --session dev-session --theme high-contrast --no-animation
```

Flags:
- `--session <id>`: Attach to or resume a specific session ID (default: `default`)
- `--theme <name>`: Color theme: `default`, `monochrome`, `high-contrast`, `no-color`
- `--no-animation`: Disable micro-animations (spinners, pulse glyphs) for reduced-motion or low-bandwidth environments
- `--no-color`: Disable all ANSI terminal colors

For keyboard shortcuts, contextual autocomplete (`Tab`, `@`, `#`), Command Palette (`Ctrl+P`), diff viewer (`d`), and full slash command reference, see [MARSHAL TUI v2 Documentation](tui.md).
