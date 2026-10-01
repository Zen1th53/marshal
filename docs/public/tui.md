# TUI commands

Use `/help` for commands and keys available to your installed version. Every
example identifier is a placeholder: copy an ID from your own session. Native
provider commands require a terminal and the installed CLI. Completion accepts
a selected candidate before submitting. Plain text alone does not launch work.

## Workspace

| Syntax | Purpose | Example |
| --- | --- | --- |
| `/help` | Display commands and keys | `/help` |
| `/status` | Inspect current session | `/status` |
| `/diff [staged\|unstaged\|untracked]` | Inspect project changes | `/diff staged` |
| `/doctor [codex\|provider]` | Diagnose the project or provider | `/doctor` |
| `/sessions` | List native conversations and governed runs | `/sessions` |
| `/update [install]` | Check or install latest release | `/update` |
| `/quit` | Exit the workspace | `/quit` |

## Marshal

| Syntax | Purpose | Example |
| --- | --- | --- |
| `/marshal [status]` | Inspect run and usage | `/marshal status` |
| `/marshal chat` | Open planning conversation | `/marshal chat` |
| `/marshal <goal>` | Draft a plan for a multi-word goal | `/marshal Add a regression test` |
| `/marshal model <codex\|claude\|agy>` | Select next planning provider | `/marshal model codex` |
| `/marshal settings [key value]` | Inspect/change next-run settings | `/marshal settings control strict` |
| `/marshal approve` | Approve the drafted plan and start | `/marshal approve` |
| `/marshal approve-task <approval-id>` | Approve a task waiting for your decision | `/marshal approve-task APPROVAL-ID` |
| `/marshal accept <task>` | Accept a task in user acceptance mode | `/marshal accept TASK-ID` |
| `/marshal return <task> <reason>` | Return a waiting task for rework | `/marshal return TASK-ID Check the error case` |
| `/marshal amend <reason>` | Request a plan amendment | `/marshal amend Add a documentation task` |
| `/marshal amend approve\|deny` | Decide a major amendment | `/marshal amend deny` |
| `/marshal stop` | Stop and keep current run state | `/marshal stop` |
| `/marshal resume` | Continue a stopped/interrupted run | `/marshal resume` |
| `/marshal close` | Close a verified run and move target branch | `/marshal close` |

## Runs and budgets

| Syntax | Purpose | Example |
| --- | --- | --- |
| `/pause [run:<id>]` | Pause new dispatch for a session run | `/pause run:RUN-ID` |
| `/resume [run:<id>]` | Resume a paused session run | `/resume run:RUN-ID` |
| `/cancel [run:<id>]` | Cancel a session run | `/cancel run:RUN-ID` |
| `/budget` | Inspect limits and measured consumption | `/budget` |
| `/budget set [calls=<n>] [duration=<duration>]` | Revise goal limits; confirmation required | `/budget set calls=20 duration=30m` |
| `/budget clear` | Revise goal to remove limits; confirmation required | `/budget clear` |
| `/mode [manual\|auto\|ultra]` | Inspect/change supervision preference | `/mode manual` |
| `/ultra [status\|start\|stop\|request]` | Inspect access or switch session execution | `/ultra status` |
| `/ultra stop confirm` | Confirm the immediately preceding stop request | `/ultra stop confirm` |

## Goals and approvals

| Syntax | Purpose | Example |
| --- | --- | --- |
| `/goal` | Inspect active goal | `/goal` |
| `/goal create <request>` | Create a goal through intake | `/goal create Add an error check` |
| `/goal edit <outcome>` | Revise goal interpretation | `/goal edit Handle empty input safely` |
| `/goal constraints` | List constraints | `/goal constraints` |
| `/goal add-constraint <text>` | Add a hard constraint | `/goal add-constraint Preserve public API` |
| `/goal rm-constraint <id\|text>` | Remove a constraint explicitly | `/goal rm-constraint CONSTRAINT-ID` |
| `/goal version [revision]` | Read goal revision | `/goal version 1` |
| `/goal diff [from to]` | Compare revisions | `/goal diff 1 2` |
| `/goal criteria` | List criteria | `/goal criteria` |
| `/goal donotdo` | List exclusions | `/goal donotdo` |
| `/goal progress` | Inspect criterion status and evidence | `/goal progress` |
| `/approvals [history]` | List pending or historical decisions | `/approvals history` |
| `/approval inspect <id>` | Read approval record | `/approval inspect APPROVAL-ID` |
| `/approval diff <id>` | Inspect commit binding and current changes | `/approval diff APPROVAL-ID` |
| `/approve <id>` | Approve exact pending record | `/approve goal:GOAL-ID@2` |
| `/reject <id> [reason]` | Reject a pending decision | `/reject APPROVAL-ID Scope needs revision` |

## Tasks and team

| Syntax | Purpose | Example |
| --- | --- | --- |
| `/tasks [list\|ownership] [--scope project\|active]` | Read project/active-plan coordination tasks | `/tasks list --scope active` |
| `/task inspect <id> [--scope project\|active]` | Inspect coordination task | `/task inspect TASK-ID` |
| `/task create <title>` | Create ready coordination task | `/task create Check empty input` |
| `/task assign <id> <agent>` | Claim task for an enabled agent | `/task assign TASK-ID AGENT-ID` |
| `/task pause <id>` | Request task pause; read settlement state | `/task pause TASK-ID` |
| `/task resume <id>` | Return a paused task to ready | `/task resume TASK-ID` |
| `/task cancel <id>` | Cancel task and request worker cancellation | `/task cancel TASK-ID` |
| `/task retry <id>` | Create next attempt of blocked task | `/task retry TASK-ID` |
| `/agents` | List participants and roles | `/agents` |
| `/msg <agent\|all> <text>` | Post to existing team session | `/msg all Please review the latest diff` |
| `/handoff <architect\|developer\|qa\|appsec> <summary>` | Propose handoff to an active role | `/handoff qa Check the error case` |

## Providers and sessions

| Syntax | Purpose | Example |
| --- | --- | --- |
| `/codex [new\|continue\|resume [id\|--last]\|fork [id\|--last]]` | Open/select a native Codex session | `/codex resume --last` |
| `/claude [new\|continue\|resume [id]\|fork [session]]` | Open/select a native Claude session | `/claude continue` |
| `/opencode [new\|continue\|resume [id\|--last]\|fork [id\|--last]]` | Open/select a native OpenCode session | `/opencode new` |
| `/agy [new\|continue\|resume [id\|--last]]` | Open/select native Antigravity; alias /antigravity | `/agy continue` |
| `/codex exec <prompt>` | Request governed Codex work | `/codex exec Check the failing test` |
| `/claude exec <prompt>` | Request governed Claude work | `/claude exec Check the failing test` |
| `/opencode run <prompt>` | Send an explicit run request | `/opencode run Check the failing test` |
| `/agy prompt <prompt>` | Open a prompted native conversation | `/agy prompt Check the failing test` |
| `/codex cli <arguments...>` | Pass provider-owned arguments; other providers also support cli | `/codex cli --help` |
| `/resume <id\|--last> [arguments...]` | Resume native Codex; run: IDs have separate meaning | `/resume --last` |
| `/fork [id\|--last]` | Fork native Codex | `/fork --last` |
| `/review [instructions]` | Open native Codex review; headless instructions unavailable | `/review Check error handling` |
| `/apply <codex_task_id>` | Apply an explicit Codex task after snapshot | `/apply CODEX_TASK_ID` |
| `/provider [status\|config <name>]` | Inspect discovery/configuration | `/provider config claude` |
| `/harness [probe\|status]` | Probe installed CLIs | `/harness probe` |
| `/models` | List discovered Codex models | `/models` |
| `/model [show]` | Inspect execution preferences | `/model show` |
| `/model select <codex\|claude> <model_name>` | Select catalogued model for future governed runs | `/model select codex MODEL_NAME` |
| `/effort [minimal\|low\|medium\|high\|xhigh\|default]` | Inspect/select supported Codex reasoning effort | `/effort default` |
| `/route [role=<role>] [harness=<name>] [risk=<R0\|R1\|R2\|R3>]` | Compute advisory route | `/route role=developer harness=codex risk=R1` |
| `/why` | Explain advisory route when ULTRA is available | `/why` |

## Provider management

These wrappers use native Codex management and require its CLI and an interactive
terminal. Consult the installed provider help for native option values.

| Syntax | Purpose | Example |
| --- | --- | --- |
| `/mcp [list\|add <name> -- <command> [args...]\|get <name>\|remove <name>]` | Manage Codex MCP servers | `/mcp list` |
| `/plugin [list\|add <name>\|remove <name>\|marketplace <arguments...>]` | Manage Codex plugins; alias `/plugins` | `/plugin list` |
| `/features [list\|enable <feature>\|disable <feature>]` | Inspect or change native Codex feature flags | `/features list` |
| `/search [on\|off]` | Open native Codex with requested search setting | `/search on` |
| `/login` | Open native Codex login | `/login` |
| `/logout` | Open native Codex logout | `/logout` |

## Recovery

| Syntax | Purpose | Example |
| --- | --- | --- |
| `/checkpoint [list]` | List project file snapshots | `/checkpoint list` |
| `/checkpoint create <reason>` | Capture project files excluding .git and .marshal | `/checkpoint create Before configuration change` |
| `/checkpoint inspect <id>` | Verify and inspect snapshot | `/checkpoint inspect CHECKPOINT-ID` |
| `/checkpoint diff <from> <to>` | Compare intact snapshots | `/checkpoint diff OLD-ID NEW-ID` |
| `/rollback <id>` | Preview file restore | `/rollback CHECKPOINT-ID` |
| `/rollback <id> confirm <digest>` | Restore after copying preview digest | `/rollback CHECKPOINT-ID confirm DIGEST` |
| `/backup create` | Create verified database backup | `/backup create` |
| `/backup restore <path> [confirm <digest>]` | Preview/confirm database restore on supported systems | `/backup restore ./backup.db` |

## Evidence, memory and diagnostics

| Syntax | Purpose | Example |
| --- | --- | --- |
| `/claims` | List active goal claims | `/claims` |
| `/claim <id> [UNSUPPORTED\|SUPPORTED\|VERIFIED\|CONTESTED\|STALE\|INVALIDATED]` | Inspect or update claim status | `/claim CLAIM-ID` |
| `/evidence list` | List artifacts; inspection verifies bytes | `/evidence list` |
| `/evidence <id>` | Inspect artifact and verification result | `/evidence ARTIFACT-ID` |
| `/inspect [claim\|evidence\|checkpoint\|task\|handoff\|approval\|agent] <id>` | Inspect canonical record | `/inspect task TASK-ID` |
| `/memory list` | List stored candidate memories | `/memory list` |
| `/memory search <query>` | Search stored memory | `/memory search error handling` |
| `/memory provenance <id>` | Inspect memory origin | `/memory provenance MEMORY-ID` |
| `/alignment [scope\|violations\|blast\|deletions\|status]` | Inspect advisory alignment checks | `/alignment status` |
| `/fingerprint` | Group recurring recorded failures | `/fingerprint` |
| `/policy [network\|sandbox\|capability\|scope\|write\|audit]` | Inspect policy | `/policy scope` |
| `/sandbox [read-only\|workspace-write]` | Inspect or request supported sandbox mode | `/sandbox` |
| `/runtime` | Inspect stored runtime state; does not prove liveness | `/runtime` |
| `/store [check quick\|check full\|counts]` | Check database integrity or inventory | `/store check quick` |
| `/skills` | List discovered skills and origins | `/skills` |
| `/skill install <name>` | Install an unambiguous verified project skill | `/skill install SKILL_NAME` |

## Additional readouts

| Syntax | Purpose | Example |
| --- | --- | --- |
| `/optimization <cycle_id>` | Inspect a recorded optimization cycle; does not activate changes | `/optimization CYCLE-ID` |
| `/alignment resolve run:<run>/<task>#<n> <acknowledged\|goal-amendment-needed> <reason>` | Record a decision on an advisory violation | `/alignment resolve run:RUN-ID/TASK-ID#1 acknowledged Reviewed the file change` |


| Syntax | Purpose | Example |
| --- | --- | --- |
| `/termination` | Read recorded goal termination; not proof of process liveness | `/termination` |
| `/context [show\|strategy]` | Inspect advisory context strategy; not applied to execution | `/context show` |
| `/verification <id>` | Inspect a verification run | `/verification VERIFICATION-ID` |
| `/reinjection` | Inspect whether session runs carry current goal constraints | `/reinjection` |
| `/export` | Write a private evidence bundle for the active goal | `/export` |
| `/memory inject [auto\|system-prompt\|project-doc\|prompt\|off]` | Inspect/change cross-provider briefing preference | `/memory inject off` |
| `/memory inject preview [claude\|codex\|opencode\|agy]` | Preview the briefing for a provider | `/memory inject preview claude` |
| `/memory inject clear` | Clear MARSHAL briefing blocks from project documents | `/memory inject clear` |
| `/memory peers <agent> <agents\|all\|none>` | Choose which peers contribute context | `/memory peers codex none` |

Exports and backups can contain project data; keep them private. Native provider
subcommands use the grammar of the qualified installed version. Unlisted
vendor-owned syntax belongs behind that provider's `cli` wrapper. `/blind` and
`/harness select` cannot perform the advertised operation in this build.

Read [Marshal mode](marshal.md), [provider boundaries](providers.md),
[recovery](recovery.md) and [known limits](concepts.md) before executing changes.
Claim labels alone do not verify evidence. A pause/cancel reply can report
settling work; check status again.

Aliases: `/?` is `/help`, `/exit` is `/quit`, `/roster` is `/agents`,
`/say` is `/msg`, and `/providers` is `/provider`.
