# Marshal v0.2.1

Marshal is now one free, open-source product: plan with one AI, let other AIs
build it, and only verified work lands on `main`. v0.2.1 is the first published
release of this line (the v0.2.0 tag was not published).

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

## Fixed since v0.2.0

- A Marshal role turn that stalls (for example after a network drop) now times
  out after 15 minutes (`MARSHAL_ROLE_TURN_TIMEOUT`), stops the run with a clear
  reason, and `/marshal resume` retries it. Runs no longer hang in REVIEWING.
- If project files change while the Marshal is planning and no run is active,
  the Board shows **NEEDS YOU** with `[u] undo` / `[K] keep`, and the Marshal is
  told to leave implementation to workers.
- Built with Go 1.26.10, which clears the standard-library vulnerabilities
  reported against Go 1.25.13.

## Known issues

- The Marshal can still occasionally start implementing a small goal itself.
  The Board now flags it and offers undo; tell the Marshal to plan instead.

## Install

Linux amd64/arm64, SHA-256 verified:

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```
