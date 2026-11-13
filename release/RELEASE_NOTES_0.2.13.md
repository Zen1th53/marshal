# Marshal v0.2.13

This release fixes what a real run of v0.2.12 showed: pausing in the middle
of a run could stop it at the final check, and a few screens still used
internal words.

## Fixed

### Pause and resume

- **A run you paused while a task was working now finishes and delivers.**
  In v0.2.12 the task finished and merged, but the run then stopped at the
  final check and nothing reached your branch.
- **If you pause and don't resume, the Marshal waits as paused** before the
  final check instead of stopping with an error.
- **"Resume all" resumes a task that was paused mid-turn right away**, and
  resuming one task that was paused before it started lets the Marshal
  continue it.
- **A run v0.2.12 left stopped** with "verification stopped: execution run
  conflict…" continues when you press Enter.
- **The Board header says "paused"** while you hold the run, and "running"
  again after you resume.

### Plain words

- When the Marshal can't finish checking the run for an internal reason, the
  Board says "Marshal could not finish checking the run" and that Enter lets
  the Marshal retry, instead of showing the raw error.
- After planning, Activity says the plan is ready, the Marshal approved it
  and the tasks start now. It no longer mentions a popup that this build
  does not have.
- When the Marshal is busy, Activity says "The Marshal was busy; sending your
  message again."
- More of the Marshal's tool results in the chat start with a plain line
  (status, tasks, agents, memory, verification) instead of raw data.

### Install

- The installer and the quick start say `cd /path/to/your/repository &&
  marshal`: opening Marshal sets the project up by itself.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

Existing installs: press `F10` in the TUI or run `marshal update install`.
