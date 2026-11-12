# Marshal v0.2.11

## Fixed

- **No failed call while the Marshal prepares the architecture.** When the
  Marshal asked for a larger page of project memories than the limit, the
  chat showed a red "arguments are not valid" failure before it retried. Any
  page size now works: a larger one reads as the largest page.
- **The Marshal can look up a task by the name the Board shows.** After a run
  closed, asking for task "greet" answered "Nothing was found". A task's short
  name now finds it in the current or last run; if two tasks share the name,
  the Marshal is told which ones to choose from.
- **You can read the "Started a new Git repository" line.** It was printed and
  at once covered by the TUI. It is still printed, and it is now also the first
  line in Activity.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

Existing installs: press `F10` in the TUI or run `marshal update install`.
