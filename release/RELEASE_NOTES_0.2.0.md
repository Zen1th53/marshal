# Marshal v0.2.0

Marshal is now one free, open-source product: plan with one AI, let other AIs
build it, and only verified work lands on `main`.

## Highlights

- **The Board.** `marshal` opens a tmux workspace with the Marshal chat on the
  left and a live Board on the right: tasks, lanes, decisions and activity.
  A first-use Board shows how a run starts; **NEEDS YOU** gates take `y`.
- **The full run loop.** Goal → architecture you accept → small tasks with
  checks → workers in isolated Git worktrees → review by a different provider
  → independent verification → closed to `main`.
- **Four providers.** Claude Code, Codex, OpenCode and Antigravity, through
  their own CLIs and your own accounts.
- **Shared project memory** with review, and a local MCP server.
- **Safer defaults.** A first-run notice explains that workers run on your
  machine as you, and `/` and your home directory are refused as project roots.

## Upgrading

State stays in `.marshal/`. v0.0.7 projects upgrade in place on first open.
Stop running sessions and back up `.marshal/` first; see
[upgrading](../docs/upgrading.md).

## Telemetry

Anonymous, opt-out telemetry goes to https://marshal.blackhat.uz: a random
installation ID, version, OS/architecture, mode and timestamps, never project
content. Disable it with `marshal telemetry disable` or
`MARSHAL_CLOUD_ENDPOINT=off`.

## Known issues

- The Marshal can occasionally start implementing a small goal itself instead
  of handing it to a worker. If its chat shows file edits before you accepted
  an architecture, tell it to plan instead.
- A run can stop progressing while the Marshal's review turn waits on a
  provider (for example after a network drop). A time limit for that turn,
  so the run reports an error and can be resumed, is planned for the next
  patch.

## Install

Linux amd64/arm64, SHA-256 verified:

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```
