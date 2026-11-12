# Marshal v0.2.8

## Fixed

- **The Marshal no longer edits your project while it plans.** With Codex or
  Claude Code as the Marshal, its chat now starts unable to write project
  files; it can write only its own plan files under `.marshal/`. Workers do all
  the edits. With OpenCode or Antigravity, which have no such switch, the Board
  says plainly when the Marshal changed files and recommends undo.
- **A finished run is delivered even when your checkout is busy.** If your
  checkout of the target branch has uncommitted changes, Marshal no longer
  stops with a git instruction: it leaves your work untouched and says exactly
  what to do ("Stash them (`git stash -u`) or undo them, then press Enter to
  finish delivering"). Untracked caches such as `__pycache__` no longer count
  as changes. A stopped run now shows its real reason instead of "The run
  stopped unexpectedly".
- **Cleanup after a run.** The run's merged worktrees and branches are removed
  when it closes; anything unmerged, changed, or your own is kept.
- **Answered cards close.** A card you answered no longer stays on screen or
  takes a later `Enter`; when a run has stopped, `Enter` goes to it.
- `y` on the architecture card now says the page is open in your browser and
  where the link is if no page appeared.
- The header says "reviewing" while the Marshal reviews a task, and the vitals
  row keeps the limit the picker just read instead of showing "limit unknown".

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

Existing installs: press `F10` in the TUI or run `marshal update install`.
