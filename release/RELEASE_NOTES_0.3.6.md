# Marshal v0.3.6

## Added

- **The Board in your browser.** Press `w` on the Board (with no task
  highlighted), pick "Open the Board in the browser" from the Space palette,
  or run `/marshal web`. The same Board opens as a web page that updates
  live: plan, lanes, tasks, workers and limits, inbox, activity, Marshal
  steps, readiness, and the decision and paused cards. It fits a phone
  screen too. It is read-only in this release: you still answer in the
  terminal Board.
  It runs only on this computer: it listens on 127.0.0.1, needs a one-time
  link token, refuses other host names, and loads nothing from the internet.
- **Advanced view for the architecture.** In the browser architecture
  review, Advanced view lets you open any part to see its own architecture,
  and parts inside that, as deep as they go. A breadcrumb and the browser's
  Back button take you up again; connections that leave the opened part
  show on its edge and take you to the other side. A part with no declared
  inner parts shows one derived from the code (its packages, then their
  files), marked as derived and never part of what you accept.

## Changed

- **An architecture you can read.** The architecture review zooms and pans
  (wheel, trackpad, pinch, drag, keys), has Fit and 100% buttons and a
  minimap, lays parts out in named bands (Interfaces, Services, Planning &
  runs, Storage & infra), draws connections as directed curves that never
  pass behind a part, groups parallel ones with a count, and shows their
  labels on hover or focus. Click a part to see it with its neighbours and a
  panel of what it does, what it covers and what flows in and out. Search
  finds a part at any level.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

Existing installs: press `F10` in the TUI or run `marshal update install`.
