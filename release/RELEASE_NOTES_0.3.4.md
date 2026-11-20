# Marshal v0.3.4

## Changed

- **The architecture lives in Marshal, not in the Marshal's prompt.** The
  Marshal's briefing carries only a pointer (revision, parts, connections);
  the Marshal reads the whole architecture from Marshal's store through its
  tools, page by page when it is large. Large projects no longer make the
  Marshal fail to start, and every Marshal turn is about 2,400 tokens lighter.
- **Any provider version, any model.** Marshal no longer carries a list of
  supported CLI versions or models. It reads what each installed CLI can do
  from its own help, and which models it offers from the CLI itself, and reads
  them again when you update the CLI.
- **One provider registry.** Names, home directories, limits and model
  catalogs come from one place for Codex, Claude, Antigravity and OpenCode.

## Fixed

- **Large architectures can be read.** A context over about 12 KiB could not
  be read through Marshal's tools at all; it is now paged.
- **Faster worker start.** A new task workspace reuses the existing project
  index (about 2.5x faster), and Codex checks are cached per installed binary.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

Existing installs: press `F10` in the TUI or run `marshal update install`.
