# FAQ

## How is this different from running Claude Code or Codex directly?

You still use them, through their own CLIs. Marshal adds what a single agent
session lacks: an architecture you accept, a separate worktree per task, review by a different
model, an independent check before merge, and one Board to watch it all.

## Does Marshal send my code anywhere?

No. Your code goes only to the providers you choose, through their CLIs.
Marshal's own anonymous telemetry sends a random installation ID, version,
OS/architecture, mode and timestamps, never code, prompts, paths or commands.
Turn it off with `marshal telemetry disable` or `MARSHAL_CLOUD_ENDPOINT=off`.
Exactly what telemetry sends, field by field, is on the [Telemetry](telemetry.md) page.

## What does it cost?

Marshal is free. Agents run on your existing provider accounts and plans.

## Which platforms?

Linux and macOS. Prebuilt, checksum-verified binaries for amd64 and arm64
(Apple Silicon included); the one-line install picks the right one. On Windows,
run it inside **WSL2** — the Linux binary installs and runs there as usual.
There is no native Windows build: the TUI requires tmux, which has no native
Windows build.

## Is it production-ready?

It is young (v0.2). The run loop ([how a run works](how-it-works.md)) is tested end to end on fresh
projects with real providers. Expect rough edges, read what you accept, and
[open an issue](https://github.com/Zen1th53/marshal/issues) when something
breaks.
