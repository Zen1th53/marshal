# Configuration

Marshal provides project-level configuration files, in-session TUI settings, CLI flags, and environment variables.

> [!WARNING]
> **Host Execution Notice**: Worker processes execute directly on your host machine under your user account without a sandbox. Worktrees isolate Git changes, not system calls or process execution. Always review goals, plans, and generated code before approving work.

---

## In-Session Settings

You can inspect and update orchestration settings directly from the Board command line by typing `/`:

### Viewing Settings

```text
/marshal settings
```

### Changing Settings

```text
/marshal settings control strict
/marshal settings rework-limit 3
/marshal settings acceptance-mode user
```

Changes apply to the **next** drafted plan.

| Setting | Choices | Default | Description |
|---|---|---|---|
| `control` | `free`, `strict` | `free` | How closely workers adhere to instructions. `free`: workers decide implementation details within assigned files. `strict`: workers follow drafted steps strictly. |
| `acceptance-mode` | `marshal`, `user`, `marshal-then-user` | `marshal-then-user` | Who authorizes task acceptance. `marshal`: the Marshal reviews and accepts. `user`: operator accepts every task. `marshal-then-user`: Marshal reviews first, then operator confirms. |
| `execution-rights` | `none`, `read-only`, `small-tasks` | `read-only` | What tasks the Marshal itself can execute directly without dispatching to worker lanes. |
| `rework-limit` | Positive integer | `2` | Maximum retry attempts for a returned task before reassigning to a different provider or escalating to the operator. |
| `ultra-concurrency` | Positive integer | `3` | Maximum number of worker tasks allowed to run simultaneously. |

---

## Project Files

When you initialize a project with `marshal init`, Marshal creates standard configuration files in your repository root:

| File | Location | Purpose |
|---|---|---|
| `CAPABILITIES.yaml` | Project root | Project capability declarations and default permissions. |
| `PACK-VERSION.yaml` | Project root | Version specification of the project pack. |
| `RUNTIME-VERSION.yaml` | Project root | Minimal runtime version requirements for the repository. |
| `.marshal/` | Project root (ignored) | Project runtime directory containing `state.db` (SQLite store), task logs, and session data. |

> [!NOTE]
> `marshal init` excludes `.marshal/` from Git tracking by appending it to `.git/info/exclude`, leaving your `.gitignore` untouched.

---

## Command-Line Flags

Marshal CLI commands accept global flags:

| Flag | Description |
|---|---|
| `--json` | Output command results as structured JSON (suitable for scripting and automation). Can be placed before or after subcommands. |
| `-h`, `--help` | Display command usage and flag descriptions without executing side effects. |

Example:

```bash
marshal --json status
marshal --json memory list
```

---

## Environment Variables

| Variable | Description |
|---|---|
| `MARSHAL_NO_UPDATE_CHECK=1` | Suppress automatic online checks for new Marshal versions. |
| `MARSHAL_PROVIDER_PATH_ONLY=1` | Restrict agent CLI discovery strictly to directories listed in `$PATH`. |
| `MARSHAL_CLOUD_ENDPOINT` | Specify the community cloud endpoint (default: `https://marshal.blackhat.uz`), or set to `off` to disable telemetry and cloud communication entirely. |
| `MARSHAL_INSTALL_DIR` | Target installation folder when running `install.sh` (default: `$HOME/.local/bin`). |
| `MARSHAL_VERSION` | Specific release tag to install with `install.sh`. |
