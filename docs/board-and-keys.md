# The Board and Keyboard Shortcuts

The Board is Marshal's terminal command center. Built on top of tmux, it coordinates tasks, worker processes, live activity streams, and operator approvals without requiring you to switch windows or manage multiple terminal tabs.

---

## Screen Layout and Navigation

The top row is the same on every screen: each provider's usage limit (where its CLI reports one) and the machine's CPU, temperature, memory, disk and running lanes.

The Board screen is divided into synchronized operational areas:

```text
+-----------------------------------------------------------------------------------+
| ✳ 42% ·4d · ⬡ 12% ·3h · ◆ —       cpu 23% · 61°C · ram 48% · disk 71% · 3 running |
+-----------------------------------------------------------------------------------+
| MARSHAL  ·  run R-7f3a  ·  DISPATCHING  ·  3/5 done  ·  inbox 1  ·  NEEDS YOU     |
+------------------------------------+----------------------------------------------+
| DOCKED CHAT PANE                   | TASKS (5 tasks, 2 running, 3 merged)         |
|                                    |   T-01  · merged   codex                     |
|  Operator: Add health endpoint     |   T-02  · running  claude                    |
|  Marshal: I have drafted a plan... |   T-03  · queued   opencode                  |
|                                    +----------------------------------------------+
|                                    | LANES (2 active workers)                     |
|                                    |   Lane 1: codex (T-02)                       |
|                                    |   Lane 2: claude (T-03)                      |
|                                    +----------------------------------------------+
|                                    | ACTIVITY                                     |
|                                    |   T-01 criteria passed (go test PASS)        |
+------------------------------------+----------------------------------------------+
| j/k move  ⏎ inspect  m/F11 chat  s say  v verify  1 board  / command  ^P palette  ?  |
+-----------------------------------------------------------------------------------+
```

---

## Focus Switching: F11 and Alt+Arrows

Marshal docks the Marshal chat pane alongside the task board for side-by-side collaboration.

- **`F11`**: Toggle keyboard focus between the Board and the docked chat pane.
- **`Alt+←`**: Move keyboard focus left from the Board to the chat pane.
- **`Alt+→`**: Move keyboard focus right from the chat pane to the Board.

When the chat pane has focus, your keystrokes type directly into the conversation with the Marshal. When the Board has focus, single-key commands navigate tasks, inspect workers, and handle approvals.

---

## Direct Input: `s` (Say)

Pressing **`s`** from anywhere on the Board (or within any view) immediately opens the "Say" composer prompt:

```text
Say to the Marshal: Add graceful shutdown to the server
```

Press `Enter` to send your message directly to the Marshal, or `Esc` to cancel. This lets you give instructions or redirect goals without having to navigate to the chat pane first.

---

## Harness Verification: `v` (Verify)

Pressing **`v`** checks the health and readiness of your AI agent providers:

- On a fresh project before runs have started, `v` arms verification for the next unverified provider shown in the first-use status bar.
- Inside the Providers view (`4`), `v` arms verification for the currently selected provider.
- Press **`Enter`** to execute the verification probe, or press any other key to cancel.

---

## NEEDS YOU Gates

When a run reaches a step that requires human authorization, the Board header displays an orange **`NEEDS YOU`** alert:

- **Architecture Gate**: When the Marshal drafts a project contract:
  ```text
  [y] accept architecture · [b] open details
  ```
- **Plan Approval Gate**: Only when the Marshal asks you to approve its plan (usually it approves the plan itself):
  ```text
  [y] approve the plan · [7] open the plan
  ```
- **Inbox Decisions**: When a worker requests confirmation or clarification.

### Resolving Gates

- Press **`y`** to arm the affirmative response.
- Press **`Enter`** to confirm and transmit the decision.
- Press any other key to cancel the armed response.

---

## Inspector Action Row

Selecting any task in the Tasks list and pressing **`Enter`** opens the **Inspector** overlay. The inspector displays the task's full execution lifecycle, assigned worker, files modified, acceptance criteria, test proofs, and timeline.

At the bottom of the inspector, the **Action Row** provides contextual one-key actions:

| Key | Button | Action |
|---|---|---|
| `a` | `[a] approve` | Approve the pending request for this task |
| `A` | `[A] approve-all` | Approve all pending requests across the run |
| `d` | `[d] deny` | Deny the pending request |
| `r` | `[r] rework` | Return the task to the worker with feedback |
| `x` | `[x] cancel` | Cancel the pending decision or task |
| `o` | `[o] open pane` | Open the live terminal pane of the task's worker |

### Navigating Inside the Inspector

- **`e`**: Toggle between compact status and all timeline events.
- **`Enter`**: Walk down the delegation chain to view child subtasks.
- **`Backspace`**: Walk back up the delegation chain to parent tasks.
- **`Esc`** or **`q`**: Close the inspector and return to the Board.

---

## Keyboard Reference

### Views

| Key | View |
|---|---|
| `1` or `Esc` | Return to the main Board |
| `2` or `h` | Host view (CPU, memory, disk, and sensor status) |
| `3` | Memory view (MCP candidates and promoted knowledge) |
| `4` | Providers view (harness health, model selection) |
| `5` | Verify view (claims, test evidence, verification status) |
| `6` | Security view (how workers run, network requests and audit records) |
| `7` | Plan view (task graph and plan versions) |
| `8` or `b` | Architecture view (project contracts and components) |

### Board Navigation

| Key | Action |
|---|---|
| `j` / `k` or `↑` / `↓` | Move selection up or down in the focused pane |
| `t` | Focus the Tasks pane |
| `i` | Focus the Inbox pane |
| `l` | Focus the Lanes pane |
| `d` | Open Git diff viewer (when inbox is empty) |
| `Enter` | Open inspector on selected task, or view lane worker |

### Global Commands

| Key | Action |
|---|---|
| `/` | Open the command-line prompt for slash commands |
| `Ctrl+P` | Open the interactive Command Palette |
| `Ctrl+X` | Stop all active workers immediately (preserves the Marshal) |
| `Ctrl+C` | Interrupt active command; press twice to exit Marshal |
| `?` | Toggle the in-app help overlay |

### Function Keys

| Key | Target |
|---|---|
| `F1` | Help overview |
| `F2` | Review view |
| `F3` | Diff viewer |
| `F4` | Status overview |
| `F5` | Models configuration |
| `F6` | MCP server overview |
| `F7` | Switch to native Codex session |
| `F8` | Switch to native Claude Code session |
| `F9` | Switch to native OpenCode session |
| `F10` | Check for or install updates |
| `F11` | Toggle focus between Board and Marshal Chat |
| `F12` | Switch to native Antigravity (`agy`) session |
