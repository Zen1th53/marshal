# MARSHAL TUI v2 — Dynamic Multi-Agent Command Center

MARSHAL TUI v2 is a terminal-first, interactive IDE, multi-agent team room, evidence console, and live command center. It operates as the complete interactive control plane over MARSHAL's canonical local runtime and SQLite store.

```text
 MARSHAL  v3 Ship dynamic TUI v2
────────────────────────────────────────────────────────────────────────────
 claude    finding
   Found a routing inconsistency in harness selection.

 codex     finding
   Editing internal/harness/router.go  +14 -5

 Team
   ● claude         architect    WORKING
   ● codex          developer    IDLE
   ● opencode       qa           IDLE
   ✗ antigravity    appsec       UNAVAILABLE

 Claims  2
   C-21       CONTESTED !  routing selects an unavailable harness
────────────────────────────────────────────────────────────────────────────
 ~/Desktop/codex/marshal │ main* │ ULTRA │ VERIFYING │ 3 ● │ C2 X1! │ $0.38
❯ @codex minimal fix, then let opencode verify█
```

The screen has four parts, each with one job:

| Part | Responsibility |
|---|---|
| **Header** | Identifies MARSHAL and the active goal, once. |
| **Body** | The live workspace: activity, team, claims, blockers. Sections collapse to a line when empty rather than reserving a pane. |
| **Statusline** | One persistent row of live context: project, git, mode, runtime state, agents, claims, cost. |
| **Composer** | Input only. It carries no status text. |

Context appears in exactly one place. The composer does not repeat what the
statusline already shows, and neither repeats the header.

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

## Dynamic Subsystem Intelligence

The TUI reflects live, canonical MARSHAL runtime state. It does not fabricate or
hardcode models, agents, versions, or claim statuses:

- **Honest Harness Probing**: Probes actual host binaries (`codex`, `claude`, `opencode`, `agy`). If a binary is missing (e.g. `agy`), it displays honest fallback: `UNAVAILABLE (agy not found)`.
- **No Invented Models**: The probe establishes whether a harness binary exists and what version it reports. It cannot read that harness's configured model, so the model column shows `UNKNOWN` for an installed harness and `UNAVAILABLE` for an absent one. A specific model name appears only when the collaboration session actually records one. `TestNoFabricatedModelsInDiscovery` and the PTY suite enforce this.
- **Honest Provider State**: `/provider status` reports probe results only. Presence of a binary is never reported as proof of authentication; credentials are `UNKNOWN` until an execution establishes otherwise.
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
| `Tab` | Open the completion popup, or advance to the next candidate |
| `Shift+Tab` | Step backward through candidates |
| `↑` / `↓` | Select a candidate while the popup is open |
| `Enter` | Accept the highlighted candidate and close the popup |
| `Esc` | Dismiss the popup, leaving the buffer as typed |

**Tab never submits.** It completes and nothing else: it does not execute the
buffer, insert a newline, reprint the prompt or touch history. A single
unambiguous candidate is completed outright; several open the popup. Accepting a
completion and running the command are two deliberate keystrokes.

Autocomplete dynamically queries live runtime state:
- **Slash Commands**: Typing `/` suggests all valid commands; fuzzy matching is supported (e.g. `/rb` suggests `/rollback`).
- **Team Agent Mentions**: Typing `@` autocompletes real active session participants (`@codex`, `@claude`, `@opencode`, etc.).
- **Object References**: Typing `#` autocompletes live canonical entity IDs (`#C-...` claims, `#E-...` evidence, `#T-...` tasks, `#CP-...` checkpoints).
- **Subcommands**: Suggests valid subcommands for `/goal`, `/budget`, `/policy`, `/provider`, etc.

### Universal Command Palette (`Ctrl+P`)

Press `Ctrl+P` anywhere in the TUI to open the fuzzy-searchable Command Palette. Every capability in the registry is indexed with category badges:
- Type to filter actions.
- Use `↑` and `↓` to navigate candidates.
- Press `Enter` to execute the selected command.
- Press `Esc` to dismiss.

### Working Tree Diff Viewer (`d`)

Press `d` from normal navigation mode to inspect unstaged/staged working tree changes:
- Displays colored unified diffs with secret redaction.
- `n` / `p`: Jump to next / previous hunk.
- `d` or `Esc`: Return to workspace.

### Screen Model

The workspace is a terminal application, not a print-and-reprompt loop. It runs
on the alternate screen and repaints in place, writing only the rows that
changed and addressing each one absolutely.

Two consequences matter:

- However many times state changes, exactly one statusline and one composer
  exist. Refreshing does not append UI fragments to scrollback.
- Exiting restores the terminal and the shell's scrollback exactly as they were.

Live events and agent state changes repaint the body while preserving the input
buffer, the cursor position and any open completion.

### Safe Interrupt & Exit

- `Ctrl+C`: Interrupts rather than quits. It closes an open overlay first, then clears pending composer input; only a second consecutive press with nothing left to interrupt exits, and any other key disarms that. Durable session state survives either way.
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
- `/approvals` — List approvals awaiting a decision; `/approvals history` shows past decisions.
- `/approval inspect <id>` — Inspect one approval record; `/approval diff <id>` shows its commit binding and the live working tree.
- `/approve [id]` — Approve pending high-risk action or out-of-scope write.
- `/reject [id]` — Reject pending approval request.
- `/termination` — Inspect the canonical termination state and reason for the active goal.

### Security, Sandbox & Providers
- `/policy` — Inspect capability broker, network egress, and gate policies.
- `/sandbox` — Inspect Bubblewrap isolation status and worktree disk budget.
- `/provider [name]` — Inspect or configure LLM provider connection health.
- `/memory` — Query durable memory fabric records and search projections.
- `/backup` — Create SQLite database snapshot.
- `/export` — Write a real evidence bundle to `.marshal/evidence/`, carrying the active goal, its critical claims and evidence refs, and a deterministic digest.
- `/context` — Show the context strategy the ULTRA router derives from the current role and risk.
- `/help` — Display interactive help and keybinding summary.

---

## Verification and Known Limitations

The TUI is verified by a pseudo-terminal (PTY) conformance suite in
`internal/tui/pty_conformance_test.go`. It builds the real binary, attaches it to
an actual terminal, writes raw key bytes, and reads what the program draws. A
command counts as operable only when it dispatches there, and every slash command
the capability registry advertises is driven through that suite.

Honest states carried by the TUI:

| State | Meaning |
|---|---|
| `UNKNOWN` | The value was not established (for example, an installed harness whose configured model MARSHAL cannot read). |
| `UNAVAILABLE` | The dependency is absent (for example, `agy` not on `PATH`). |
| `BLOCKED_BY_POLICY` | A security policy prevents the operation. |
| `NOT_AVAILABLE` | The subsystem exposes no readable state to the TUI. |

Current limitations, stated rather than hidden:

- **Provider egress is blocked by design.** `marshal run` executes harnesses inside
  a bubblewrap cell built with `--unshare-net`, because per-endpoint egress cannot
  be enforced without a filtering proxy. A harness needing API access therefore
  blocks inside the cell. This is fail-closed behaviour and is reported as
  `BLOCKED_BY_POLICY`, never as availability.
- **Antigravity headless execution is unavailable** unless the `agy` CLI is
  installed. The Antigravity desktop IDE is not a headless harness.
- **Failure fingerprints are not persisted.** The registry in `internal/epistemic`
  is per-run and in-memory, so `/fingerprint` reports `NOT_AVAILABLE` rather than
  asserting a clean result it cannot establish.
- **Backup restore is not performed from a live session**, since it would swap the
  database out from under an open workspace. `/backup restore` verifies the
  artifact and directs the operator to the offline path.
