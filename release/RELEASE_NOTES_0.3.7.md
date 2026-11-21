# Marshal v0.3.7

## Added

- **Open a part with one click.** In the browser architecture review, a
  single click (or tap, or Enter) on a part that has something inside opens
  it, with the zoom-in, the breadcrumb and Back. Its panel stays open inside.
  A part with nothing inside still just focuses.
- **Ask about any element, however small.** In Advanced view you can mark a
  package, a file, a declaration or a connection on any level, including the
  levels derived from code (Shift-click, the pencil, or Select area for
  several), and send the Marshal a question or change request about exactly
  that. The request carries the element's path and kind, shows as a dot on
  it and in History. A request about derived code never changes the
  reviewed architecture and never blocks your Accept.

## Fixed

- **A reassigned task can start again.** Moving a task to another worker
  (or an automatic provider failover) after the plan had moved on was
  refused on every worker ("task lane admission refused: durable task lane
  binding differs"), so the run stopped. The new attempt now starts from the
  current integration head; a foreign lane or a plain rework at another base
  is still refused.
- **`/marshal resume` says why it waits.** When the run waits on your own
  pause it now says that `/resume` lifts it, instead of "a Marshal
  operation is already running".

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

Existing installs: press `F10` in the TUI or run `marshal update install`.
