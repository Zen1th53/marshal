# MARSHAL TUI v2 — Dynamic Multi-Agent Command Center

MARSHAL TUI v2 is a terminal-first, interactive IDE, multi-agent team room, evidence console, and live command center. It operates as the complete interactive control plane over MARSHAL's canonical local runtime and SQLite store.

## Native Claude sessions

Run `marshal claude` or press F8 in MARSHAL to open native Claude Code.
`marshal claude --continue` continues its latest conversation;
`marshal claude --resume` opens Claude's picker. Native CLI arguments are passed
through, including `--model`, `--permission-mode`, attachments and configuration
options. Within MARSHAL, use `/claude new`, `/claude continue`, `/claude resume`,
`/claude fork [session]`, or `/claude cli <arguments>`.

Plain composer text runs nothing. Launching an agent spends tokens and can touch
the worktree, so it happens only when the operator names one: `/codex <prompt>`,
`/claude <prompt>`, `/opencode <prompt>`, or F7/F8/F9 for a native session. A command typed without its
leading slash is answered with the command it looks like, not with a session.

Native Claude uses the operator's existing `CLAUDE_CONFIG_DIR` (normally
`~/.claude`), authentication, skills, MCP servers and plugins. MARSHAL imports
visible user/assistant text from this project's Claude JSONL histories every two
seconds and on exit. Records pass the memory secret firewall and are saved as
agent-authority candidates in MARSHAL's database. Thinking blocks are excluded by
construction. `.marshal/claude/history-index.json` tracks completed imports
across restarts. `/memory list` and `/memory search <query>` include both providers.

The native window uses Claude's own permission controls. `/claude exec` and
`/claude run` retain the existing governed stream-json execution path. Native
resume still uses Claude's history storage; MARSHAL's copied conversation memory
does not depend on retaining that original session after import.

## Native OpenCode sessions

Run `marshal opencode` or press F9 in MARSHAL to open the installed OpenCode TUI.
Native arguments pass through unchanged. Within MARSHAL, `/opencode new` starts a
fresh session, `/opencode continue` resumes the latest session, and `/opencode
resume <session>` or `/opencode fork <session>` selects or forks a session.
`/opencode cli <arguments>` exposes the remaining native CLI surface, including
models, providers, authentication, MCP servers, agents, stats and batch runs.

OpenCode uses the operator's existing configuration and authentication. MARSHAL
reads `opencode export <session>` through OpenCode's public CLI after the native
process exits, then selects only visible conversation and bounded tool evidence
for agent-authority candidate memory. Reasoning, snapshots and provider metadata
are excluded, and the normal memory firewall still rejects secrets.
`.marshal/opencode/history-index.json` prevents an unchanged export from being
imported again. Cross-agent briefings use the same bounded `AGENTS.md` block and
incoming live inbox mechanism as Codex.

## Tool capture

A native session records its tool calls alongside its conversation, because what
an agent ran and changed is the part a later session needs and a summary of it is
not. Each call is stored with its name, the argument that says what it acted on,
and the source it wrote or deleted rendered as a diff. Tool results are stored
truncated to 2 KiB with their true size stated inline, so a truncated payload is
never mistaken for a complete one; source written or removed is kept whole up to
64 KiB. Records are labelled `[assistant:tool_use]` and `[user:tool_result]` in
the body. Hidden reasoning is still never stored, whatever the setting.

Capture is on for native sessions only. The generic `ImportProviderHistory` path
stays conversation-only, so importing a history MARSHAL did not supervise cannot
pull tool payloads into memory by accident.

## Cross-agent memory injection

Each CLI resumes only its own history, so a starting agent knows nothing about
what the other did in the same project. Before launching one, MARSHAL compiles
the stored sessions of the *other* providers into a bounded briefing (8 KiB, most
recent sessions first) and delivers it over a channel that provider supports.
The briefing states that its contents are observations rather than verified facts.

| Channel | Delivery | Cost |
| --- | --- | --- |
| `auto` | Claude: `system-prompt`; Codex: `project-doc` | default |
| `system-prompt` | `--append-system-prompt` (Claude only) | no turn |
| `project-doc` | a marked block in `AGENTS.md` / `CLAUDE.md` | no turn, touches the worktree |
| `prompt` | the opening prompt | one turn and its tokens |
| `off` | nothing is delivered | — |

`/memory inject` shows the setting and what each provider resolves to,
`/memory inject <channel>` changes it, `/memory inject preview [claude\|codex]`
prints the briefing a provider would receive, and `/memory inject clear` removes
MARSHAL's block from the project documents. The setting is stored in
`.marshal/inject-channel`.

The `project-doc` channel owns exactly the text between its
`<!-- marshal:memory:start -->` and `<!-- marshal:memory:end -->` markers and
leaves the rest of the file byte-identical; a file with only one of the two
markers is refused rather than repaired by guesswork. A configured channel the
provider cannot honour falls back and reports the fallback instead of silently
delivering nothing. An operator's own `--append-system-prompt`, or their own
opening prompt, is never overridden. Injection failures are reported and never
block the session: an agent with no briefing is the earlier behaviour, not a
broken one.

### Live cross-agent exchange

The launch briefing is a snapshot: it says what the other providers had done by
the time this session started, and goes stale while the session is open.

So while an agent runs, MARSHAL also watches the **other** providers' histories
for this project. Work they do now is imported into memory as it happens and
appended to this session's inbox at `.marshal/inbox/<provider>.md`. The inbox is
truncated when the session opens, so it only ever carries what arrived since.

A running CLI owns the terminal, so MARSHAL cannot inject anything into it.
Delivery is a **pull**: the briefing names the inbox file and says plainly that
nothing pushes it, so the agent reads it when it needs current context. The
inbox carries the same untrusted-data framing the briefing does.

The inbox is bounded at 64 KiB with a 1 KiB cap per entry. On reaching the
budget it stops appending and says so in the file, so a truncated inbox is never
mistaken for a quiet one. The count of delivered updates is reported when the
session exits.

Peer watchers keep their own index (`.marshal/<running>/peer-<other>-index.json`)
and are primed against existing history at launch, so two MARSHAL sessions
watching the same provider do not fight over one file and the inbox does not
replay history the briefing already summarized.

Injection delivers a compiled briefing, not the full memory store. A new session
still does not automatically receive everything MARSHAL has stored; use
`/memory search <query>` for the rest.

## Native Codex sessions

Run `marshal codex` to open the installed Codex inside a MARSHAL session. Native
arguments pass through unchanged, for example:

```bash
marshal codex resume --last
marshal codex -i "screenshots/error state.png" "Fix this error"
marshal codex --model MODEL
```

In the MARSHAL TUI, F7 opens Codex from either navigation or the composer. A plain
prompt opens a new native conversation. `/codex continue` resumes the latest
Codex session for the current directory; `/codex resume` opens its session picker.
`/codex cli <arguments>` passes native options and subcommands, with quoted paths
and prompts supported. Exit Codex to return to MARSHAL. Use `/codex new` for a
fresh conversation.

The native interface provides Codex's own streaming conversation, attachments,
model and reasoning selection, skills, MCP, plugins, approvals and other native
controls. It uses the installed binary and the operator's existing `CODEX_HOME`,
authentication and configuration. MARSHAL does not replace that configuration
with its config-free task environment. Native CLI errors remain errors.

MARSHAL imports user messages, final assistant answers and tool calls from local
Codex JSONL history every two seconds and on exit. Records go through the existing
secret firewall into the project's SQLite memory as **agent-authority candidates**;
they are not verified facts. `/memory list` and `/memory search <query>` find
them. A private `.marshal/codex/history-index.json` records completed imports so
reopening does not duplicate unchanged history. Changed histories for the same
project are recovered after interruption. Hidden reasoning and credentials are not copied
into memory; tool payloads are captured under the bounds described in **Tool
capture**. An import failure is reported on return.

Native thread state remains in Codex's own storage, which its resume/fork commands
use. MARSHAL's memory is a portable record of visible conversation, not a copy of
Codex's internal process state. Remote-only histories and non-JSONL history formats
are not captured by this watcher. A JSONL event over 1 MiB is reported as an import
error rather than silently truncated.

Native sessions use **Codex's approval and sandbox controls**. They are not
Process 05 governed tasks and do not receive MARSHAL task attestations. Explicit
`/codex exec` and `/codex run` retain that separate governed execution workflow.
The PTY regression test verifies native argv, exclusive keyboard ownership,
return to MARSHAL and memory persistence using a deterministic child process;
it does not certify every upstream cloud or account-dependent feature.

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

### Startup surface and the navigation gate

`marshal tui` opens on the **composer**, which every session has. The frozen-IA
navigation surface (Home, Control, Status, Work, Verify, Memory, Models,
Security, System) is an **ULTRA** feature: `Ctrl+N` and `Esc` on an empty
composer open it only when the session holds a verified ULTRA entitlement.
Without one, both entry points refuse and say so, and every MARSHAL command
stays available from the composer. `/ultra` reports why a session is Standard;
`/ultra request` asks an operator for an entitlement.

The gate is read live on each attempt rather than captured at startup, because
the Cloud handshake finishes after the workspace is built: a session that
becomes entitled mid-run can open navigation without restarting.

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

### Paste and copy

Pasted text arrives as data, never as keystrokes, so a pasted newline is
inserted rather than submitting the buffer.

A paste of four or more lines, or over 800 bytes, collapses to a placeholder —
`[Pasted text #1 +42 lines]` — and is restored verbatim when the draft is
submitted. Shorter pastes are inserted inline. A placeholder the operator typed
themselves has no paste behind it and is left exactly as written.

Mouse reporting is held **only while the navigation view is open**, which is the
only surface that reads a mouse event. On the composer the terminal keeps its
own selection, so selecting and copying text works normally.

### Contextual Autocomplete

| Keybinding | Action |
|---|---|
| typing `/`, `@` or `#` | Opens the menu as you type, and narrows it as you continue |
| `↑` / `↓` | Move the highlight; the draft is left exactly as typed |
| `Tab` | Accept the highlighted candidate into the draft |
| `Shift+Tab` | Move the highlight backwards |
| `Enter` | Accept the highlighted command and run it |
| `Esc` | Dismiss the menu, leaving the buffer as typed |

The menu is not summoned, it follows the buffer: typing a trigger opens it,
typing on narrows it, and typing past every candidate closes it. A menu offering
exactly the word already typed closes too, so a finished command stays
submittable rather than having Enter taken away from it.

Moving the highlight never writes to the draft. Only `Tab` and `Enter` do, and
the highlight survives narrowing, so typing one more letter cannot silently
select a different command than the one under the cursor.

**Tab never submits.** It completes and nothing else: it does not execute the
buffer, insert a newline, reprint the prompt or touch history. `Enter` is the
key that runs a command, whether the menu is open or not.

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
