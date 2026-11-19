# Marshal v0.2.19

v0.2.18 was tagged but not published (a test caught the Board's background
agy listing before the build); everything below ships in v0.2.19.

This release comes from running Marshal on itself with Antigravity as the
Marshal, as a real owner would, on a Mac. It makes setup one step, counts
every install, lets any provider version and any agy model work, makes
Antigravity a full Marshal, and says the truth when something cannot run.

## Added

### One-step setup

- **The installer sets the machine up.** It adds Marshal to `PATH` in your
  shell's startup file, checks Git and tmux 3.3a+ (installing them with
  Homebrew on macOS, and asking before any `sudo` on Linux), lists the coding
  agents it finds, and ends with what is left and the command to start.
- **Every install is counted**, anonymously and once, right after installing
  (`marshal telemetry register`), instead of only after Marshal was first
  opened. `marshal telemetry disable` and `MARSHAL_CLOUD_ENDPOINT=off` still
  turn it off.

### Antigravity

- **Name a need, not a model.** `auto`, `best`, `fast`, `plan`, or a family
  (`carbon`, `argon`, `hydrogen`, `flash`, `orchestrator`) is resolved against
  what your signed-in agy offers now; with Google AI Ultra `auto` is the Carbon
  Orchestrator. `/model select agy best` stores the selector, so a new model is
  used without a Marshal update.
- **Antigravity is a full Marshal.** Started with `marshal agy`, it gets
  Marshal's tools and can submit the architecture and the plan; it no longer
  stops at the intake. It is not asked to approve every Marshal tool.

### Limits you can trust

- **Claude's limits are shown again** (5-hour and weekly % left and reset
  time); Claude Code stopped writing them where Marshal used to read them.
- **agy shows which model families can run** and which ones are spent, instead
  of "limit unknown".

## Changed

- **No provider is held to a version list.** A provider is used at whatever
  version passes its check, and when a provider updates itself Marshal checks
  it again on its own instead of asking you to run `/harness verify`.

## Fixed

- **On a Mac, the Marshal's proposals reach the Board** (they did only on
  Linux), so `y` opens the architecture.
- **"This computer cannot run governed workers"** is said at once on macOS and
  Linux arm64, instead of "network problem", retries and providers cooling
  down. Governed workers need Linux amd64; see the FAQ.
- **An architecture the browser cannot draw is refused when the Marshal sends
  it**, with the rule, instead of failing when you press `y`.
- **A decision waits while the Marshal is still in its turn** instead of being
  re-issued under new ids that a slow Marshal then chased.
- **After an update, Marshal offers to restart the control centre** that kept
  running the previous version.
- `v` and `/harness verify agy` verify Antigravity on every platform, verify
  the provider that leads first, and say which model will run; `marshal agy`
  no longer asks "Who plans?" with another provider preselected.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

Existing installs: press `F10` in the TUI or run `marshal update install`.
