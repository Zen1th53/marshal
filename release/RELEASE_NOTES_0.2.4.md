# Marshal v0.2.4

## Fixed

- **Telemetry reports a start at launch on slow connections.** The background
  telemetry worker shared one network budget between registration and the
  first `app_start`/`heartbeat`. On a slow link registration used most of it,
  so the start was dropped and only reported after the next heartbeat interval
  (2 minutes). Registration and the event flush now each get their own bound,
  so a start is reported right away. Each individual network attempt still
  stays within the same timeout, and the offline opt-out and shutdown bounds
  are unchanged.

## Known issues

- When every installed provider is a worker in the same run, no independent
  verifier is left and the run cannot close. Keep one provider out of the
  worker list for now.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

Existing installs: press `F10` in the TUI or run `marshal update install`.
