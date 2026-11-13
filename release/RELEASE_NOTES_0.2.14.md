# Marshal v0.2.14

This release fixes the final check of an ULTRA run. Real runs whose tasks
were done by Antigravity stopped after every task was merged, and nothing
reached your branch.

## Fixed

### The final check

- **ULTRA runs whose tasks are done by Antigravity (or Claude) now finish
  their final check and deliver.** The independent checker was Codex, whose
  answer cannot name the model that gave it, so the check always stopped
  with "REQUIRED_CONTEXT_REVIEW_PENDING". The check now goes to a provider
  that can name its model (Claude Code); the check itself is unchanged.
- **When no independent checker is available**, the run stops before it
  spends a model turn and says what to do: install Claude Code or wait until
  its limit passes (it must not be doing this run's tasks), then press Enter.
- **A final-check stop reads in plain words** with Enter to retry, including
  stops recorded by earlier versions, instead of an internal code.

### The Board

- **The header says "paused" as soon as you pause**, and "running" as soon
  as you resume. In v0.2.13 it changed only when something else happened.
- **No false "Verifying Antigravity failed"** right after "Antigravity
  verified."
- **Earlier Codex work for the project still loads** when a session file of
  another project in the Codex history cannot be read; if loading fails,
  Activity says so plainly and that the run does not need it.

## Known limit

- In this build only Claude Code can be the final ULTRA checker, so it must
  be installed and must not be one of the run's workers.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

Existing installs: press `F10` in the TUI or run `marshal update install`.
