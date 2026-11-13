# Marshal v0.2.15

This release fixes what a long real project run showed: a refused project
summary, a plan change that could not be applied, a restart that would not
start, and a final check that did not say why it stopped.

## Fixed

### Architecture

- **Opening the architecture before the Marshal sent its project summary**
  now says so in plain words and asks the Marshal to send it, instead of
  "REQUIRED_CONTEXT_REVIEW_PENDING".
- **When the project summary is refused, the Marshal is told exactly why**
  (for example a folder path ending in "/"), so it can fix it and send it again.
- **A memory the Marshal saved itself can no longer silently block the
  architecture**: the Marshal is told which memory it is and to state the rule
  in the architecture instead.

### Plan changes and restarts

- **A plan change the Marshal decides is applied once** and is not asked again.
- **A plan change whose reason is too long says so**, with its length and the
  500-byte limit, instead of a silent refusal.
- **Running the same plan again with `/marshal use-plan`** no longer stops with
  "its workspace didn't match the approved plan".
- **When a task reaches its rework limit, the stop tells you how to go on**:
  move it to another worker, or press Enter to give it one more attempt. The
  run keeps its finished work.
- **When the independent reviewer turns down work the Marshal accepted**, the
  reason says the review was the cause.

### The final check

- **When the final check does not accept a run, the Board says which goal
  criteria it rejected and why**, instead of "VERIFICATION_FAILED". A run
  stopped this way by an older build reads in plain words.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

Existing installs: press `F10` in the TUI or run `marshal update install`.
