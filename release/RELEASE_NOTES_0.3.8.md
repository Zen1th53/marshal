# Marshal v0.3.8

## Added

- **Run Marshal from the browser.** The Web Board now acts, not only shows.
  The page is simple: one line of what is happening, one line of what is
  needed from you, and when something waits for you, one big button (for
  example **Continue the run** or **Retry rv06**), with at most two more.
  Type to the Marshal in **Tell the Marshal…**. With no card on screen you
  can still **Retry** a stuck task or **Stop all workers** (two clicks).
  Everything else is under **Details**. Every button does exactly what the
  same key does in the terminal Board, and Activity notes "From the web".
  It still runs only on this computer (127.0.0.1, a one-time link, no
  cookies, exact origin checks, one-use action tokens).

## Fixed

- **A paused run no longer spins.** After a restart, a run whose execution
  was still paused re-sent the same task hundreds of times and then said it
  "stopped making progress" with nothing to resume. It now stops at once
  with the plain reason, and Enter continues it.
- **The raw error shows the cause.** `d` on a paused card shows the recorded
  technical cause and is drawn only when there is one.
- The simulated test harness can no longer be selected by a production run.

## Changed

- **About 25,000 fewer lines.** Code that no shipped build reached was
  removed, new projects no longer create 40 unused tables (existing ones are
  untouched), and duplicated logic was merged. Nothing you use changes.
- MCP and A2A report the real Marshal version, and
  `marshal architecture serve` opens your browser on macOS.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

Existing installs: press `F10` in the TUI or run `marshal update install`.
