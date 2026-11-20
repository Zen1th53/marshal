# Marshal v0.3.3

v0.3.2 was tagged but not published (a governed-edition test failed before the
build); everything below ships in v0.3.3, plus:

- **Workers keep their lease while they are prepared.** On a slow machine the
  worker lease used to run out while Marshal was still preparing the worker,
  so every attempt stopped after five minutes; it is now kept until the worker
  runs, and from then on by the worker's own heartbeats.
- **An explicit Codex model is checked first.** A model you name is checked
  against what your installed Codex offers before the task is claimed.

This release comes from running Marshal on itself in a Linux VM until a real
run stayed up: every fix below was found in that run.

## Added

- **The Board tells the truth about workers.** A lane shows while its worker
  sends heartbeats, with elapsed time and "last signal 12s ago". A worker silent
  for two minutes shows as lost, and a run with no live worker says "not
  running: no worker for Nm" instead of "running".
- **You see what the Marshal decides for you.** If the Marshal tries to accept
  the architecture in your place, Marshal refuses it and tells you; if it
  approves a plan itself (ultra mode), the Board says so once.

## Changed

- **Any model Codex offers.** Workers no longer check a fixed model list; they
  use what the installed Codex reports and fall back to your default model.
- **Slow machines are fine.** tmux calls get longer deadlines and one retry for
  read-only commands; a slow terminal is reported as such and never cools a
  provider down.

## Fixed

- **The Codex Marshal starts after the architecture grows.** Its hidden
  instructions passed a fixed size limit once the living architecture grew;
  the limit is higher and a context that still does not fit is read through
  Marshal's tools. A failed Marshal start now says why.
- **The Marshal chat cannot touch Marshal's state database.** Enforced for agy,
  Codex and Claude; state is read through Marshal's tools only.
- **Retries no longer break a task.** Infrastructure retries and interrupted
  attempts no longer use up a task's lane attempts.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

Existing installs: press `F10` in the TUI or run `marshal update install`.
