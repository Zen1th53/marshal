# Marshal v0.4.0

The first MVP release: a short terminal home, the Web Board as the full
interface, and a run that goes from your goal to merged work on a Mac.

## Changed

- **The terminal is one short home screen.** It shows the project and its
  state, **Now** (what is happening), **From you** (nothing, or the one thing
  that needs you) and **Web** (where the Web Board is). `Enter` always opens
  the Marshal chat. A safe step (retry a stuck task, resume, confirm the
  requirements) is `y`. Decisions that need your authority, such as accepting
  the architecture, are made on the web.
- **The Web Board is the full interface.** It opens in your browser when the
  Board starts (`MARSHAL_WEB_OPEN=0` keeps it closed). Its sections are Home,
  Work, Architecture, Team, Activity and Settings. Security, Verify and Memory
  are shown as coming soon.
- **Limits per model group.** Team shows each provider's limits per model
  family and window. agy's model groups are tracked separately, so one
  exhausted group no longer blocks the others.
- **The final check always has a checker.** The final check needs a tool that
  names the model it answers with and did none of the run's tasks. Today that
  is Claude Code. Marshal keeps it off worker tasks when it plans, fails over
  or reassigns, so a run can always land.
- The classic Board and the Board v2 terminal panes are removed.

## Fixed

- **macOS runs work end to end.** Workers started from the Board failed at
  once on a Mac ("worker terminal transport unavailable"). They now run, and
  four tasks build in parallel.
- **Accepting the architecture works from any terminal.** When Marshal was
  started inside a Claude Code session, Accept was refused with "Open the
  Marshal chat". After an Accept, the Marshal was also not told. Both are
  fixed.
- **No silent stalls.** Three cases used to stop the run with no message:
  - A refused plan now gives the Marshal a specific reason and shows
    **FIXING PLAN**.
  - An imported plan now wakes the Marshal to approve it.
  - A run that does not move for 5 minutes becomes a needs-you item with its
    reason.
- **Finished work is not thrown away.** If Git has no `user.name` or
  `user.email`, the run now pauses before any worker starts and says how to
  set them. Before, a finished task was recorded as a crash and run up to
  seven times. The Board's Raw error now shows the real cause.
- On a Mac, agy is no longer offered as a worker, because it cannot take
  tasks there.
- A message pasted into a narrow Claude Code chat is no longer left unsent.
- Commands sent while another one runs wait in a queue instead of being
  refused.
- macOS: `marshal update install` installs the darwin archive. Commands that
  talk to `marshal daemon` work, and workers' Marshal MCP server starts.

## Security

- Host workers get an allowlisted environment. `TMUX*`, `GITHUB_TOKEN`,
  `AWS_SECRET*`, `SSH_PRIVATE*` and `DOCKER_AUTH*` never reach a worker.
  Side effects:
  - Owner-started sessions lose `SSH_AUTH_SOCK` and `AWS_*`.
  - Claude through Bedrock does not authenticate on host workers.
  - Toolchain settings other than Go's are dropped.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

Existing installs: press `F10` in the TUI or run `marshal update install`.
