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
- **Every owner card on the new Board.** Requirements, plan changes, guards
  and the other cards that used to fall back to the classic Board are now
  decision cards over the dimmed Board, with the same facts, keys and clicks.
  Esc puts a decision off while it waits.
- **Marshal steps.** The steps pane lists what the Marshal did, one short
  line per tool call (read the context, checked the architecture, recalled
  memory...). The lines are kept in a small log, so they survive a Board
  restart. Prompts and submitted text are never written there.
- **Usage counts and agy sign-in from the Board.** On the readiness screen,
  `y` keeps usage counts on, `t` turns them off and `?` shows what is sent.
  When agy is installed but not signed in, `g` opens it in a new tmux window
  and the Board checks it again when you close that window.

## Changed

- **The chat pane is titled MARSHAL CHAT**, and the title stays even when the
  provider's CLI tries to set its own.
- **Quieter activity.** Repeated activity lines are folded into one, so the
  feed shows what changed.
- **Lighter Marshal turns.** The list of Marshal's tools sent with every
  Marshal turn is smaller.
- **Every key shown works.** A key the Board draws now does what its label
  says, and a key with no action is not drawn. On a card, the key bar shows
  that card's keys.
- **Panes fit their content.** Panes no longer stretch to the bottom of the
  screen; the chat shows "input clear" once the Board has looked at it.

## Fixed

- **Decisions reach the Marshal.** With Claude Code as the Marshal, a
  decision could sit in the chat as `[Pasted text #N]` and never be sent, so
  planning stalled and the decision expired. Decisions are now submitted
  within a second; your own half-typed text is never sent.
- **A restarted Marshal chat stays with its run.** A resumed or restarted
  chat is bound to the run again, and open requests carry over.
- **Telemetry recovers by itself.** If the telemetry server no longer knows
  your installation, Marshal registers it again once instead of failing.
- **An accepted architecture is applied.** If the Marshal chat restarted
  after you pressed Accept in the browser, the Marshal could not use your
  Accept, so planning stayed at "tasks" while the Board said the
  architecture was accepted. Your Accept now covers that exact architecture
  (same revision and content) whatever chat sent it, and the Board and the
  Marshal read the same record. A project already stuck this way is fixed
  the next time Marshal opens; an Accept that cannot be applied is logged
  with the reason.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

Existing installs: press `F10` in the TUI or run `marshal update install`.
