# Marshal v0.2.5

## Added

- **macOS builds.** Releases now ship `darwin/amd64` and `darwin/arm64`
  binaries. The one-line install works on Intel and Apple Silicon Macs with no
  extra step: a `curl | sh` install is not Gatekeeper-quarantined, and the
  arm64 binary is ad-hoc signed by the Go toolchain, so no Apple signing or
  notarization is needed.

  ```bash
  curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
  ```

- **A clear warning when no coding tool is installed.** Startup now checks for
  a supported provider CLI (`claude`, `codex`, `opencode` or `agy`). With none
  installed Marshal still opens, but it says so up front and `marshal doctor`
  reports it, instead of the problem only surfacing at the first run.

## Documentation

- The telemetry docs now show the exact wire format: the two endpoints, example
  JSON for registration and events, and every field that is sent (README and
  `docs/telemetry.md`).

## Known issues

- When every installed provider is a worker in the same run, no independent
  verifier is left and the run cannot close. Keep one provider out of the
  worker list for now.
- On macOS, the host-watch CPU/temperature panel reads Linux counters and
  shows nothing; the rest of Marshal works.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

Existing installs: press `F10` in the TUI or run `marshal update install`.
