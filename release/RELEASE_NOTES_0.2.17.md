# Marshal v0.2.17

This release fixes what broke when a plan had to change in the middle of a
real run, and two things that got in the way of starting Marshal.

## Fixed

### Changing a plan mid-run

- **A plan change keeps finished work.** Tasks that were merged or accepted and
  are not changed by the amendment keep their state and commits, instead of
  starting over.
- **A plan change keeps the run's budget**, so workers no longer stop at the
  start with "budget contract defines no MaxTasks limit".
- **A refused plan or change says how to do it**: name an existing task first
  (`/marshal amend <task-id> <change>`), with an example and the run's task IDs.

### Holding a task

- **A held task really waits.** A hold now stops the task from starting in the
  run it is meant for, and a change the Marshal makes on its own no longer
  releases it.
- **"Hold until I release it"** is shown on the Board with a key to release it,
  survives a restart, and every hold or release is written to the Marshal's inbox.

### Starting Marshal

- **Marshal starts inside another tmux session** instead of stopping with "running
  inside another tmux session".
- **An old usage reading no longer blocks choosing a provider**; it is shown as
  possibly out of date.
- **A refused requirements card names the line and the rule**, for example a line
  that is too long.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

Existing installs: press `F10` in the TUI or run `marshal update install`.
