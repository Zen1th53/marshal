# Terminal commands

These commands run in your terminal, outside the MARSHAL window. Run them in
your project folder. `marshal help` lists all of them.

| Command | What it does |
| --- | --- |
| `marshal` | Open the MARSHAL window (same as `marshal tui`). |
| `marshal tui` | Open the MARSHAL window. |
| `marshal version` | Show which version is installed. |
| `marshal init` | Set up MARSHAL in this project. |
| `marshal doctor` | Check that everything is set up. Add `--probe-providers` to also check your agents. |
| `marshal update` | Check for a new version. `marshal update install` installs it. |
| `marshal codex`, `marshal claude`, `marshal opencode`, `marshal agy` | Open an agent directly. |
| `marshal state backup --output FILE` | Back up MARSHAL's records. |
| `marshal state verify-backup FILE` | Check that a backup is intact. |
| `marshal state restore FILE` | Restore a backup. Close all MARSHAL windows first. |
| `marshal daemon` | Start MARSHAL's local service in this terminal. Some terminal-only commands need it. Stop it before restoring a backup. |

Most people only need `marshal init`, `marshal doctor` and `marshal tui`.
Everything else happens inside the MARSHAL window: see the
[command reference](commands.md).

Add `--json` before a command to get machine-readable output from commands
that support it.
