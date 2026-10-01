# 0.0.5 release notes

This page summarizes the user-facing changes in the 0.0.5-rc.5 release notes.
The candidate comes from the 0.0.5 integration branch. Install candidates
deliberately; automatic updates follow the latest release.

## Planning and delivery

- `/marshal chat` opens a planning conversation; bare `/marshal` displays status.
- The Marshal asks about language, goal and project needs, then writes a plan pack.
- Review the requirements, index and task notes before `/marshal approve`.
- Workers use isolated worktrees and receive their approved task notes.
- Choose `free` or `strict` control and the acceptance mode before a run.
- Return work for changes with `/marshal return <task> <reason>` and resume it.
- Separate review sessions allow one installed provider to fill multiple roles.

## Commands and recovery

- Completion accepts the chosen candidate before executing a command.
- Mistyped subcommands and malformed arguments are refused.
- Goal edits expose revision history and require a fresh confirmation.
- Pause, resume and cancel act on session runs; call and duration budgets stop dispatch.
- Model and effort choices apply to future governed work.
- Checkpoints can be listed, verified, compared and restored with a preview.
- Database backup restore is coordinated on Linux; offline restore remains available.
- `/sessions` separates native conversations from governed runs and selects latest
  conversations within the current project.
- `/apply` requires a Codex task ID, snapshots first and reports actual changed files.
- `/diff` supports staged, unstaged and untracked changes.

## ULTRA and limits

ULTRA execution starts off in every session. `/ultra start` enables it when
available; stopping requires `/ultra stop` followed immediately by
`/ultra stop confirm`. Status reports availability and execution separately.

Alignment is advisory, navigation is closed, OpenCode is a worker rather than a
Marshal model, live database restore is Linux-only, and provider qualification
is limited to [specific versions](providers.md). Keep the window open during a run.
The release record does not establish real-model completion of the full workflow.
See [concepts and known limits](concepts.md).
