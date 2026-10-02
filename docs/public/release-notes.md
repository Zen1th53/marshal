# What's new in 0.0.5

## Plan with the Marshal

- `/marshal chat` opens a conversation with the Marshal. It asks about your
  goal, reads your project, and writes a plan you can read and edit.
- Agents follow the approved plan. Each task runs in its own copy of your
  project, and tasks that depend on others start from their finished work.
- You choose how strictly agents follow the plan, and who accepts the work.
- Send a task back with `/marshal return` and a note on what to fix.
- One installed agent is enough. MARSHAL gives each role its own session.

## Commands that now work

Many commands that only printed "not available" in earlier versions now work:

- edit your goal and see its history,
- approve or reject requests,
- pause, resume and cancel runs, and set limits on calls and time,
- choose the model and, for Codex, how hard it thinks,
- save, compare and restore checkpoints,
- restore a backup without leaving MARSHAL (Linux),
- see past conversations per project with `/sessions`,
- apply a Codex cloud task and see exactly what changed,
- see staged, unstaged and untracked changes with `/diff`.

## Easier to use

- A typo in a command now does nothing and suggests the right command.
- Enter accepts the highlighted suggestion first.
- `/marshal` on its own shows the status instead of starting a session.

## ULTRA

- ULTRA is switched on and off from inside MARSHAL with `/ultra start` and
  `/ultra stop`.
- `/ultra` shows when your access ends and, if access is refused, why.

See [What MARSHAL cannot do yet](limits.md) for the known limits of this
version.
