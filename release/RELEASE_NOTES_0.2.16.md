# Marshal v0.2.16

This release fixes two blockers that showed up when the architecture or the
plan had to change partway through a real project.

## Fixed

### Architecture changes

- **A new revision of an accepted architecture now opens in the browser.**
  Before, pressing `y` always showed the revision you had already accepted, so
  a change could never be accepted.
- **A revision that is missing the fields needed to replace the current one is
  refused when it is sent**, with the names of the missing fields, instead of
  failing later after you accept it.

### Plan changes

- **When the Marshal sends a plan that is refused, the refusal names the
  project read it is missing**, so it can read it and send the plan again.
- **Proposals left over from a run that has ended are closed**, so they no
  longer show up as waiting.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

Existing installs: press `F10` in the TUI or run `marshal update install`.
