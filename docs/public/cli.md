# CLI reference

Run commands in the project directory. `marshal help` lists the installed CLI
surface. The entry point is `marshal [--json] <command> [arguments]`;
`--json` requests structured output for commands supporting it.

| Syntax | Purpose | Example |
| --- | --- | --- |
| `marshal version` | Report build version | `marshal version` |
| `marshal init` | Initialize missing project defaults and local state | `marshal init` |
| `marshal doctor [--probe-providers]` | Diagnose prerequisites; optionally probe providers | `marshal doctor --probe-providers` |
| `marshal adapters` | List provider discovery | `marshal adapters` |
| `marshal adapter probe <name>` | Probe an adapter | `marshal adapter probe codex` |
| `marshal tui [session-id]` | Open the terminal workspace | `marshal tui work` |
| `marshal codex [arguments...]` | Open native Codex | `marshal codex resume --last` |
| `marshal claude [arguments...]` | Open native Claude | `marshal claude --continue` |
| `marshal opencode [arguments...]` | Open native OpenCode | `marshal opencode` |
| `marshal agy [arguments...]` | Open native Antigravity | `marshal agy` |
| `marshal tasks` | List stored tasks | `marshal tasks` |
| `marshal task show <id>` | Inspect a task | `marshal task show TASK-ID` |
| `marshal task import <file> [--dry-run]` | Validate/import task definitions | `marshal task import tasks.json --dry-run` |
| `marshal run <task> --adapter <name> [--model <model>] [--agent <id>]` | Request governed execution | `marshal run TASK-ID --adapter codex` |
| `marshal logs <task>` | Inspect task logs | `marshal logs TASK-ID` |
| `marshal cancel <task>` | Cancel task execution | `marshal cancel TASK-ID` |
| `marshal state backup --output <file>` | Back up project state | `marshal state backup --output ./backup.db` |
| `marshal state verify-backup <file>` | Verify a backup | `marshal state verify-backup ./backup.db` |
| `marshal state restore <file>` | Restore offline after closing windows and daemon | `marshal state restore ./backup.db` |
| `marshal update [install]` | Check or install latest release | `marshal update` |

Identifiers and filenames above are placeholders; use records from your own
project. Bare `marshal` opens the TUI in an interactive terminal.
For interactive work use the [TUI reference](tui.md). See
[recovery](recovery.md) before restoring and [provider setup](providers.md)
for execution boundaries. CLI status and task execution can require the local
runtime; `marshal daemon` starts it in the foreground and `marshal status`
queries it. Stop it before offline restore.
