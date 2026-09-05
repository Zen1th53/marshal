# MARSHAL TUI v2 — Dynamic Multi-Agent Command Center

MARSHAL TUI v2 is a terminal-first, interactive IDE, multi-agent team room, evidence console, and live command center. It operates as the complete interactive control plane over MARSHAL's canonical local runtime and SQLite store.

```text
╭─ MARSHAL ─ marshal ─ ULTRA ─ VERIFYING ─────────────── main · clean ─╮
│ Goal 100%   Claims 2/2   ● 4 active   Budget $0.00   Risk R0         │
╰──────────────────────────────────────────────────────────────────────╯
┌─ LIVE WORKSPACE ──────────────────────┬─ ACTIVE TEAM ────────────────┐
│ CODEX · developer                     │ ● Codex       AVAILABLE      │
│ Implemented fix for auth race         │ ● Claude      AVAILABLE      │
│   internal/auth/session.go +18 -7     │ ● OpenCode    AVAILABLE      │
│                                       │ ○ Antigravity UNAVAILABLE    │
│ MARSHAL                               │   (agy not found)            │
│ C-01 · CRITICAL · SUPPORTED           │                              │
│   E-01 ✓ race test passed             │ Task owner: Codex            │
├───────────────────────────────────────┼───────────────────────────────┤
│ CURRENT TOOL                          │ EVIDENCE                      │
│ go test -race ./...                   │ C-01 SUPPORTED                │
│ ● 0.4s elapsed                        │ └─ E-01 ✓ test-output.txt     │
└───────────────────────────────────────┴───────────────────────────────┘
[MARSHAL]-[marshal]-[ULTRA|READY]
>>> 
```

---

## Launching the TUI

Start the TUI interactively:

```bash
# Explicit command
marshal tui

# Or simply (launches TUI when attached to an interactive terminal)
marshal
```

### CLI Flags

| Flag | Description | Default |
|---|---|---|
| `--session <id>` | Attach to or resume a specific session ID | `default` |
| `--theme <name>` | Theme palette: `default`, `monochrome`, `high-contrast`, `no-color` | `default` |
| `--no-animation` | Disable micro-animations (spinners, pulse effects) for low-overhead or reduced-motion environments | `false` |
| `--no-color` | Disable ANSI terminal color codes | `false` |

---

## 100% Dynamic Subsystem Intelligence

The TUI reflects live, canonical MARSHAL runtime state. It **never** fabricates or hardcodes models, agents, versions, or claim statuses:

- **Honest Harness Probing**: Probes actual host binaries (`codex`, `claude`, `opencode`, `agy`). If a binary is missing (e.g. `agy`), it displays honest fallback: `UNAVAILABLE (agy not found)`.
- **Live Git State**: Directly inspects the current repository worktree for branch name, commit SHA, and uncommitted modification counts.
- **Silence-by-Default Chatter Filtering**: High-frequency tool noise and chatter are collapsed into structured summary cards rather than flooding the conversation workspace.
- **Canonical 6-Core Integration**: Direct access to Epistemic Claim Graph, Alignment Guard, Blind Interpretation, Checkpoints & Rollback, Budgets, and Re-injection.

---

## Keyboard-First Navigation

The TUI is fully operable without a mouse.

### Line Editing & Composer

| Keybinding | Action |
|---|---|
| `←` / `→` | Move cursor character-by-character |
| `Home` / `Ctrl+A` | Move cursor to start of line |
| `End` / `Ctrl+E` | Move cursor to end of line |
| `Backspace` / `Delete` | Remove character before/after cursor |
| `Ctrl+W` | Delete preceding word |
| `↑` / `↓` | Navigate command history buffer |
| `Ctrl+R` | Interactive reverse history search |
| Bracketed Paste | Safe multi-line and clipboard text paste without accidental execution |

### Contextual Autocomplete

| Keybinding | Action |
|---|---|
| `Tab` | Trigger completion / cycle forward through suggestions |
| `Shift+Tab` | Cycle backward through suggestions |

Autocomplete dynamically queries live runtime state:
- **Slash Commands**: Typing `/` suggests all valid commands; fuzzy matching is supported (e.g. `/rb` suggests `/rollback`).
- **Team Agent Mentions**: Typing `@` autocompletes real active session participants (`@codex`, `@claude`, `@opencode`, etc.).
- **Object References**: Typing `#` autocompletes live canonical entity IDs (`#C-...` claims, `#E-...` evidence, `#T-...` tasks, `#CP-...` checkpoints).
- **Subcommands**: Suggests valid subcommands for `/goal`, `/budget`, `/policy`, `/provider`, etc.

### Universal Command Palette (`Ctrl+P`)

Press `Ctrl+P` anywhere in the TUI to open the fuzzy-searchable Command Palette. All 100 MARSHAL capabilities are indexed with category badges:
- Type to filter actions.
- Use `↑` and `↓` to navigate candidates.
- Press `Enter` to execute the selected command.
- Press `Esc` to dismiss.

### Working Tree Diff Viewer (`d`)

Press `d` from normal navigation mode to inspect unstaged/staged working tree changes:
- Displays colored unified diffs with secret redaction.
- `n` / `p`: Jump to next / previous hunk.
- `d` or `Esc`: Return to workspace.

### Safe Interrupt & Exit

- `Ctrl+C`: Opens a safe interrupt dialogue if an operation or agent execution is running, preventing accidental process death and preserving durable session state.
- `/quit` or `Ctrl+D`: Cleanly exits the TUI without terminating durable background daemon tasks.

---

## Complete Command Reference

Every user-operable MARSHAL capability has a direct command mapping:

### Session & Workspace
- `/status` — Inspect active runtime, schema, task, and participant counts.
- `/msg <text>` — Broadcast message to workspace or `@agent` specifically.
- `/pause` — Pause active workflow execution.
- `/resume` — Resume paused workflow.
- `/cancel` — Cancel active workflow.
- `/doctor` — Run system health diagnostics across SQLite, runtime socket, and harnesses.
- `/runtime` — Inspect local daemon socket and active execution leases.
- `/store` — Inspect SQLite database schema version, tables, and integrity.
- `/diff` — Open interactive working tree diff viewer.
- `/quit` — Exit the TUI workspace.

### Epistemic Claims & Evidence
- `/claims` — List all claims in the canonical epistemic ledger.
- `/claim <id> [UNSUPPORTED|SUPPORTED|VERIFIED|CONTESTED|STALE|INVALIDATED]` — Inspect or update claim status.
- `/evidence` — List evidence artifacts with provenance and verification hashes.
- `/inspect <id>` — Inspect claim or evidence detail with contradiction checks.

### Tasks & Team Management
- `/tasks` — List coordination tasks, owners, and states.
- `/task <id> [state]` — Inspect or update task coordination state.
- `/agents` — List registered team participants, assigned roles, and harness statuses.

### Goal, Alignment & Constraints
- `/goal [text]` — View or update session objective and success criteria.
- `/alignment` — Inspect Alignment Guard scope boundaries and blast radius.
- `/reinjection` — Inspect constraint re-injection integrity and handoff hashes.
- `/blind` — Inspect blind interpretation state and divergence checks.

### Routing, ULTRA & Harnesses
- `/route` — Inspect harness selection rationale and capability match.
- `/ultra [on|off]` — Toggle ULTRA intelligent dynamic routing.
- `/harness [name]` — Inspect or select preferred harness.
- `/model [name]` — Set model selection for active harness.
- `/effort [low|medium|high]` — Configure native reasoning effort.
- `/fingerprint` — Inspect failure fingerprinting and normalized error vectors.

### Governance, Budgets & Approvals
- `/budget [amount]` — Inspect or update token/cost/time budget limits.
- `/checkpoint [name]` — Create durable checkpoint of worktree and epistemic state.
- `/rollback [id]` — Roll back worktree and claims to a prior checkpoint.
- `/approve [id]` — Approve pending high-risk action or out-of-scope write.
- `/reject [id]` — Reject pending approval request.

### Security, Sandbox & Providers
- `/policy` — Inspect capability broker, network egress, and gate policies.
- `/sandbox` — Inspect Bubblewrap isolation status and worktree disk budget.
- `/provider [name]` — Inspect or configure LLM provider connection health.
- `/memory` — Query durable memory fabric records and search projections.
- `/backup` — Create SQLite database snapshot.
- `/export` — Export structured evidence bundle and audit ledger.
- `/help` — Display interactive help and keybinding summary.
