# Marshal v0.3.5

## Added

- **A new Board: the control centre.** The Board is redrawn as titled panes
  on one grid. You see one screen for each moment of a run: readiness before
  you start, planning, the command centre while workers run, a decision card
  when the Marshal needs your answer, a paused card when a run stops, the
  Marshal chat strip, and a split view with the chat beside the Board. Two
  columns from 120 columns wide, stacked below. If you need the old Board,
  `MARSHAL_BOARD=classic` keeps it for this one release.
- **Readiness says why.** Before you start, each provider shows its state
  in plain words: a failing agy check says agy is not signed in, and the
  provider serving as the Marshal is marked "in use", so you know what to fix
  and what is already taken.
- **One-key recovery.** When a run pauses, the paused card offers the way to
  continue on a single key.
- **Core actions first.** The Space palette lists the eight actions you use
  most before everything else.

## Changed

- **The chat pane is titled MARSHAL CHAT**, and the title stays even when the
  provider's CLI tries to set its own.
- **Quieter activity.** Repeated activity lines are folded into one, so the
  feed shows what changed.
- **Lighter Marshal turns.** The list of Marshal's tools sent with every
  Marshal turn is smaller.

## Fixed

- **Decisions reach the Marshal.** With Claude Code as the Marshal, a
  decision could sit in the chat as `[Pasted text #N]` and never be sent, so
  planning stalled and the decision expired. Decisions are now submitted
  within a second; your own half-typed text is never sent.
- **A restarted Marshal chat stays with its run.** A resumed or restarted
  chat is bound to the run again, and open requests carry over.
- **Telemetry recovers by itself.** If the telemetry server no longer knows
  your installation, Marshal registers it again once instead of failing.
- **Tests build on macOS again.**
- <!-- arch-apply fix -->
- <!-- board steps/heights fix -->

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

Existing installs: press `F10` in the TUI or run `marshal update install`.
