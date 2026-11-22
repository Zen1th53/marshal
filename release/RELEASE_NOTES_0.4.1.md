# Marshal v0.4.1

Independent review on every plan, the accepted architecture carried into
every task, and fixes found by driving a real run through the Web Board.

## Changed

- **Independent review on every plan.** Each hand-in gets an independent
  cross-review and every run a final independent check, on Standard plans as
  well as ULTRA: by another provider when one is free, otherwise by a fresh
  session of the same one.
- **The architecture is carried into the work.** Each worker is told its part
  of the accepted architecture. Every review must say whether the code
  conforms, and new code outside every part goes back with the exact places.
- **Design review runs from Marshal.** The independent design review is
  launched by Marshal, not from the Marshal's own shell.

## Fixed

- **Codex reviews no longer stop the run.** The review schema was refused by
  Codex's strict mode, so every run stopped after its first hand-in.
- **A reopened project shows its last run** instead of "No run yet".
- **A restart no longer strands a review**; a run that stops moving says so.
- **Web Board:** you see the requirement lines you confirm, the Marshal's
  questions are not hidden behind the architecture card, timers are honest
  and a stopped run shows why.
- **Architecture view:** parts stay inside their columns, every column is
  labelled, and a revised architecture keeps its revision number.
- **No false "signed out"** for a provider that is signed in.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

Existing installs: press `F10` in the TUI or run `marshal update install`.
