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
the worktree, so it happens only when the operator names one: `/codex exec <prompt>`,
`/claude exec <prompt>`, `/opencode run <prompt>`, or F7/F8/F9 for a native session. A command typed without its
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

## Native Antigravity sessions

Run `marshal agy` (or `marshal antigravity`) or press F12 in MARSHAL to open the
installed Antigravity CLI, `agy`. Native arguments pass through unchanged.
Within MARSHAL, `/agy new` starts a fresh conversation, `/agy continue` resumes
the most recent one, `/agy resume <conversation>` selects one by ID, and
`/agy prompt <prompt>` opens the session with that prompt. `/agy cli <arguments>`
exposes the rest of the native CLI, including models, agents, MCP servers and
plugins. `/antigravity` is the same command.

agy uses the operator's existing configuration and sign-in. It keeps each
conversation as its own SQLite database under
`~/.gemini/antigravity-cli/conversations`, and has no export command, so MARSHAL
reads those databases directly and read-only once the native process exits. It
never writes to agy's files. Only fields observed to carry visible conversation
and tool evidence are read: the user's input, the agent's visible answer, tool
calls with their arguments, and command output or failure text. The model's
reasoning sits in a separate field that is never read, and the normal memory
firewall still rejects secrets.

Conversations that existed before the launch are recorded first, so the exit
sync imports only what this session created or continued. A conversation agy
recorded against a different workspace is left to that project.
`.marshal/antigravity/history-index.json` prevents an unchanged conversation
from being imported again. Cross-agent briefings reach agy through the same
`AGENTS.md` block as Codex and OpenCode; agy reads it from the workspace.

agy's storage format is not published. If a later release changes it, fields
MARSHAL cannot find are skipped rather than guessed at, so capture loses
evidence instead of inventing it.

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

In the MARSHAL TUI, F7 opens Codex from either navigation or the composer. A quoted
prompt such as `/codex "fix the bug"` opens a new native conversation. `/codex continue` resumes the latest
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
Security, System) is **not available yet**: it has declared screens with no
capability behind them and has not been verified end to end, so `Ctrl+N` and
`Esc` on an empty composer refuse for every session, entitled or not, and say
so. Navigation shortcuts are shown in the activity panel and `/help` only
when navigation is released and the session is entitled. Every MARSHAL command
stays available from the composer. Once it is
verified it becomes an **ULTRA** feature, opened only when the session holds a
verified ULTRA entitlement. `/ultra status` reports whether verified ULTRA
execution is active, entitled but switched off, or unavailable. It shows the
Cloud grant's end in local time separately from the short, renewable session
lease; older Cloud servers report the grant end as unavailable. The header and
statusline show `ULTRA ACTIVE` only while entitlement and execution are both
enabled; an entitled session with execution off shows `ULTRA EXEC OFF`. `/ultra`
is a short alias for the same status.
`/ultra request` asks an operator for an entitlement.

Every session starts with execution off. `/ultra start` turns it on for the
session when a verified entitlement is held, and says how to request one when
it is not. `/ultra stop` asks first; `/ultra stop confirm`, typed as the very
next command, turns execution off. No environment variable switches execution
on.

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

Function keys F1–F10 and F12 have the actions shown in `/help`; F11 is unassigned.
F3 uses the same diff action as `/diff`, including a recovery hint when Git fails.

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
| `Tab` / `Shift+Tab` | Cycle candidates forward / backward into the draft; never submit |
| `Enter` | Accept the highlighted candidate without running; submit a finished command when no selection was made |
| `Esc` | Dismiss the menu, leaving the buffer as typed |

The menu is not summoned, it follows the buffer: typing a trigger opens it,
typing on narrows it, and typing past every candidate closes it. A menu offering
exactly the word already typed closes too, so a finished command stays
submittable rather than having Enter taken away from it.

Moving the highlight with arrows never writes to the draft. `Tab`, `Shift+Tab`
and accepting with `Enter` do. The highlight survives narrowing, so typing one more letter cannot silently
select a different command than the one under the cursor.

**Tab never submits.** It completes and nothing else: it does not execute the
buffer, insert a newline, reprint the prompt or touch history. `Enter` is the
key that runs a command when the menu is closed. With the menu open, it accepts
and closes the menu; a second Enter runs the command. A finished command with
a trailing space (for example `/goal `) runs immediately if no candidate was
explicitly selected. After arrows or Tab select a candidate, Enter accepts it
instead. Editing the text or closing the menu resets that explicit selection.

Bare `/marshal` shows the current Marshal panel status and command usage.
`/marshal chat` explicitly opens a Marshal conversation; `/marshal <goal>`
drafts a plan using the Marshal model. Completion lists every Marshal
subcommand, with `chat` first. Fixed-form actions (`help`, `status`, `approve`,
`close`, `stop`, `resume`, `chat`, `use-plan`) reject extra arguments before
acting. Marshal model choices, amendment decisions, settings keys and enum values
also complete at their argument positions. Planning without an attached project
runtime is refused before a run is created. Failed closure, amendment, approval and resume
operations retain the last task snapshot when no new canonical run is available.

`/memory inject` and `/memory peers` use local project configuration and remain
available even when the workspace has no database store. `/memory inject`
rejects extra arguments instead of silently changing the channel or clearing
project document blocks. Peer author lists reject empty lists and `none` mixed
with other names. `/memory peers participants none` is refused: the channel
format currently treats an empty participant set as all agents.
`/memory provenance <id>` looks up the project record directly, including records
older than the list window.

Autocomplete dynamically queries live runtime state:
- **Slash Commands**: Typing `/` suggests all valid commands; fuzzy matching is supported (e.g. `/rb` suggests `/rollback`).
- **Team Agent Mentions**: Typing `@` autocompletes real active session participants (`@codex`, `@claude`, `@opencode`, etc.).
- **Object References**: Typing `#` autocompletes live canonical entity IDs (`#C-...` claims, `#E-...` evidence, `#T-...` tasks, `#CP-...` checkpoints).
- **Subcommands**: Suggests valid subcommands for `/goal`, `/task`, `/checkpoint`, `/policy`, `/provider`, etc.

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

Failed commands retain any handler output and recovery hints alongside the error.

Live events and agent state changes repaint the body while preserving the input
buffer, the cursor position and any open completion.

### Safe Interrupt & Exit

- `Ctrl+C`: Interrupts rather than quits. It closes an open overlay first, then clears pending composer input; only a second consecutive press with nothing left to interrupt exits, and any other key disarms that. Durable session state survives either way.
- `/quit` or `Ctrl+D`: Cleanly exits the TUI without terminating durable background daemon tasks. Accepted command names are case insensitive, including `/QUIT` and `/EXIT`; malformed quit commands show usage and keep the workspace open. Exit preserves any already-durable data and does not claim that a workspace without a store has a durable session.

---

## Complete Command Reference

Every user-operable MARSHAL capability has a direct command mapping:

### Session & Workspace
- `/update [install]` — Check for a newer published release, or install it. `F10` does the same: it installs the release the notice is showing, and checks when there is none.
- `/status` — Show canonical session, goal, team, claim, budget, and termination status. Runtime execution is not verified by this command.
- `/msg <agent|all> <text>` — Messaging is unavailable in TUI; authenticated runtime authorization is required.
- `/pause` — Reports unavailable authenticated runtime process control; does not pause execution.
- `/resume` — Reports unavailable runtime process control. `/resume <id|--last> [native arguments...]` opens native Codex resume when its CLI is installed.
- `/cancel` — Reports unavailable authenticated runtime process control; does not cancel execution.
- `/doctor [codex|provider]` — Run system diagnostics, or native Codex diagnostics in an interactive terminal. Failed project checks include an initialization/diagnostic hint; optional provider probes are not run by bare `/doctor`.
- `/runtime` — Report the session label and that runtime execution health is NOT VERIFIED; the TUI has no authenticated health channel.
- `/store` — Read the SQLite schema version. Integrity is NOT VERIFIED by a schema read; unavailable or failed stores include a recovery hint.
- `/diff` — Open the interactive working tree diff viewer. Extra arguments are rejected; failed Git inspection includes a worktree recovery hint.
- `/quit` — Exit the TUI workspace.

### Agents & Integrations

- `/codex`, `/claude`, `/opencode`, and `/agy` (`/antigravity`) open their native session in an interactive terminal. `status` and `help` inspect availability; `cli <arguments...>` passes native arguments with quoted paths and IDs supported.
- `/opencode resume [id|--last]` and `/opencode fork [id|--last]`, and `/agy resume [id|--last]`, preserve native options supplied after the session selector. `new` and `continue` reject extra arguments instead of discarding them.
- `/codex <prompt...>` opens native Codex in the TUI. `/codex exec <prompt...>` and `/codex run <task_id> [model]` retain governed task execution; Claude exposes the same governed operations.
- `/claude model <slug>` selects the governed model and supports repeated changes. CLI flags are rejected as model names.
- `/mcp [list|add <name> -- <command> [args...]|get <name>|remove <name>]` manages Codex MCP servers. `rm` and `delete` alias `remove`.
- `/plugin` and `/plugins` expose Codex plugin listing and native management. Governed `add`/`install` and `remove`/`rm`/`uninstall` require one plugin name; `marketplace <arguments...>` passes through to Codex.
- `/skills` (or `/codex skills`) lists local Codex skills. Incomplete plugin discovery is reported with a recovery hint while successfully discovered local skills remain visible. `/skill install <name>` (or `/codex skill install <name>`) installs a local skill with digest verification.
- `/sessions` lists governed Codex session records. `/apply [task_id]` invokes Codex apply; without an ID it selects a task from governed session history and reports history failures before proceeding.
- `/fork [id|--last]` opens native Codex fork in the TUI. `/resume --last` opens native Codex resume; bare `/resume` retains the unavailable runtime-control meaning described above.
- `/review [instructions]` opens native Codex review in the TUI. Without an interactive terminal, governed commit review supports no instructions and explicitly reports that limitation when instructions are supplied.
- Native management requires the provider CLI and an interactive terminal. In particular, Claude MCP/plugin/auth/agents/login/logout commands report that requirement in headless use; they do not create governed tasks.

### Epistemic Claims & Evidence
- `/claims` — List claims for the active goal revision with epistemic verification states.
- `/claim <id> [UNSUPPORTED|SUPPORTED|VERIFIED|CONTESTED|STALE|INVALIDATED]` — Inspect or update claim status.
- `/evidence <id>` — Show the evidence link and supporting or contradicting claim in the active claim set; reports NOT FOUND for unknown evidence.
- `/inspect [claim|evidence|checkpoint|task|handoff|approval|agent] <id>` — Inspect a canonical record, or infer its kind from the identifier.

### Tasks & Team Management
- `/tasks` — List coordination tasks, owners, and states.
- `/task [list|inspect <id>|ownership]` — Read coordination tasks. `/tasks` accepts the same subcommands. `create`, `assign`, `pause`, `resume`, `cancel`, and `retry` are recognized but mutations are unavailable without authenticated runtime authorization.
- `/agents` — List registered team participants, assigned roles, and harness statuses.

### Goal, Alignment & Constraints
- `/goal` — View the active goal. `/goal constraints` lists its constraints. Goal edits require authenticated runtime authorization and are unavailable. Reporting for `diff`, `version`, `criteria`, `donotdo`, and `progress` is not implemented. Free text does not update the goal.
- `/mode [manual|auto|ultra]` — Inspect or switch the session supervision mode label; ULTRA requires a verified entitlement.
- `/handoff <architect|developer|qa|appsec> <summary>` — Unavailable without authenticated runtime authorization.
- `/alignment [scope|violations|blast|deletions|status|resolve [reason ...]]` — Report alignment NOT VERIFIED. Resolution is unavailable without authenticated runtime authorization; malformed inspection arguments return usage.
- `/reinjection` — Report the execution-bound constraint digest NOT VERIFIED; extra arguments return usage.
- `/blind [resolve [reason ...]]` — Report interpretation NOT VERIFIED; resolution is unavailable. Unknown subcommands return usage.

### Routing, ULTRA & Harnesses
- `/route [role=<architect|developer|qa|appsec>] [harness=<name>] [risk=<R0|R1|R2|R3>]` — Compute an advisory route; never applies it to Runtime.
- `/why` — Explain advisory routing when a verified ULTRA entitlement is active.
- `/ultra [status|start|stop|request]` — Show ULTRA status, switch execution on or off for the session, or request an entitlement.
- `/harness [probe|status|select <role> <harness>]` — Probe CLI availability. Selection is recognized but NOT applied because authenticated runtime execution-profile integration is unavailable.
- `/model [show]` — Read saved harness model preferences without applying them. `/model <codex_slug>` selects a Codex execution model through its control authority; `/model select <harness> <model_name>` is recognized but NOT applied without runtime execution-profile integration.
- `/models` — List discovered Codex models and selection through the control authority; extra arguments return usage.
- `/effort [low|medium|high]` — Read probed reasoning knobs and the advisory route default, with actual selected effort UNKNOWN. Capability metadata is not a saved preference. Changes are recognized but NOT applied without runtime execution-profile integration.
- `/fingerprint` — Report per-run failure fingerprint history NOT_AVAILABLE; it is not persisted for this session.

### Governance, Budgets & Approvals
- `/budget` — Inspect consumed budget; missing token and cost measurements are UNKNOWN. Does not update limits.
- `/checkpoint [list|create|inspect|diff]` — Recognized but unavailable: authenticated runtime snapshot support is not implemented.
- `/rollback <id>` — Reports that rollback was NOT performed; authenticated runtime restoration is not implemented.
- `/approvals` — List approvals awaiting a decision; `/approvals history` shows past decisions.
- `/approval inspect <id>` — Inspect one approval record; `/approval diff <id>` shows its commit binding and the live working tree.
- `/approve [id]` — Unavailable without authenticated runtime authorization; does not grant approval.
- `/reject [id]` — Unavailable without authenticated runtime authorization; does not deny approval.
- `/termination` — Inspect the canonical termination state and reason for the active goal.

### Security, Sandbox & Providers
- `/policy [network|sandbox|capability|scope|write|audit]` — Report policy enforcement NOT VERIFIED. No policy is changed; unknown or extra arguments return usage.
- `/sandbox` — Report runtime isolation NOT VERIFIED. `/sandbox <read-only|workspace-write>` opens native Codex with that sandbox mode and the saved Codex model preference in an interactive terminal; it does not persist a policy.
- `/provider [status|config <name>]` — Inspect harness availability, with authentication UNKNOWN until established by execution. Inline credentials are explicitly refused. `/providers` is an alias.
- `/memory` — Query durable memory fabric records and search projections.
- `/backup create` — Write and verify a SQLite snapshot under `.marshal/backups/`, retaining distinct files for rapid repeated calls. `/backup restore <backup_path>` verifies an artifact and directs you to offline `marshal state restore`; quote paths containing spaces. Bare `/backup` shows usage.
- `/export` — Refresh canonical state, then write an evidence bundle for the current goal revision to `.marshal/evidence/`, with critical claims, evidence refs, and a deterministic digest. Without a goal, it explains the required canonical session rather than recommending an unavailable TUI edit. Extra arguments return usage.
- `/optimization <cycle_id>` — Read a canonical optimization cycle and its decisions, vetoes, blocked actions, and digest. Missing records and store failures include a recovery hint.
- `/features [list|enable <feature>|disable <feature>]` — List or toggle native Codex feature flags. Bare `/features` lists; malformed arguments return usage.
- `/search [on|off]` — Open native Codex with the requested search setting and saved Codex model preference in an interactive terminal. Accepted aliases are `enable`/`true` and `disable`/`false`; no setting is persisted by this command.
- `/login`, `/logout` — Open native Codex authentication commands in an interactive terminal. Without a terminal, authentication stays unchanged. Extra arguments return usage.
- `/context` — Show the context strategy the ULTRA router derives from the current role and risk.
- `/verification <id>` — Inspect a canonical verification run.
- `/help` (alias `/?`) — Display interactive help and keybinding summary.

---

## Update notice

When a newer release is published, the workspace says so in the activity panel
with the key that installs it:

```
 Update  MARSHAL v0.0.3 is available  [F10] Download and install · /update
```

The check behind it reads the public release feed, runs off the input loop so a
slow feed cannot delay the workspace, and installs nothing. A check that fails
is not reported: a workspace that opened is not the place to explain that GitHub
was unreachable. `MARSHAL_NO_UPDATE_CHECK=1` turns it off entirely.

Installing is the user's action, never the check's. `F10` installs the release
the notice is showing and, when there is no notice, checks for one; `/update
install` does the same from the composer. The archive is verified against the
release's published checksum before anything is replaced, and a session that
updates keeps running the build it started with until it is restarted.

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

Provider slash commands use an explicit grammar. Bare `/codex`, `/claude`,
`/opencode`, and `/agy` (also `/antigravity`) still open their native sessions
in a terminal. Send work with `/codex exec <text>`, `/claude exec <text>`,
`/opencode run <text>`, or `/agy prompt <text>`. A quoted first argument,
for example `/codex "fix the bug"`, opens a native session with that prompt
(Codex and Claude use governed execution when no terminal is attached).
Unknown first words run nothing. Typos within edit distance two suggest a
known subcommand and explain the prompt syntax. `/agy fork` is unsupported;
use `/agy cli <native arguments>` for vendor-owned syntax. All providers'
`cli` escape hatches preserve native argument values and ordering. Command
aliases are normalized by the same application table in terminal and batch
launches: Codex plugin `install` becomes `add`, while Claude and agy plugin
`remove`/`rm` become `uninstall`; MCP `rm`/`delete` become `remove` where
that provider has a removal verb. OpenCode `auth` becomes `providers`.

Provider completion and `/help` use a version-qualified dialect record.
The offline CLI help currently qualifies only Codex **0.159.2**, Claude
**2.1.286**, OpenCode **1.18.16**, and agy **1.2.7** (singleton version
ranges). Other versions and unlisted operations stay **UNKNOWN**. Help labels
them **unqualified pass-through via cli**; completion still offers them, with
that label, so a CLI version MARSHAL has not qualified keeps its commands.
Operations shown not to exist for the qualified version are withheld. The `cli` completion itself identifies its arguments as
unqualified pass-through. Explicit unqualified commands retain their existing
execution paths and display that qualification status. Known **UNSUPPORTED**
operations are refused before launch, including through `cli` and after
fixed-arity global options. An unknown vendor operation is never treated as
a prompt by MARSHAL's provider-command parser.
Raw CLI positional values are checked only at recognized native command
positions: Claude's prompt `review`, OpenCode's project directory `review`,
and agy's positional `fork` remain UNKNOWN pass-through values.

Codex uses `resume`/`fork` in a terminal and `exec resume`/`exec fork` in
headless wrappers. Named headless forks require a session ID. Latest-session
`fork --last` is terminal-only: the qualified version has no `exec fork
--last` flag. Resume supports `--last` in both modes. These forms, and
`exec review`, are backed by the qualified Codex version's help. Governed `/review` still reviews the current commit and
does not accept custom instructions. Claude session/management wrappers and
OpenCode/agy native wrappers remain **terminal-only in MARSHAL**, even when
the vendor has a separate batch grammar. Help and completion mark that
boundary. MARSHAL's local model, task, and history services are identified
separately from vendor grammar.

Qualification observes only bounded `--version` probes (two seconds, 64 KiB,
no stdin); help evidence is checked in, never discovered by running a session.
Completion refresh invalidates cached qualification when the executable's
identity, modification time, size, or mode changes. This record establishes
grammar support, not authentication, session success, or sandbox/network
enforcement. Existing execution authorization and isolation checks remain
in force. `MARSHAL_PROVIDER_PATH_ONLY=1` opts into PATH-only discovery for
isolated runs; normal discovery retains its existing fallback directories.
See [qualification evidence](testing/provider-dialects/README.md).
