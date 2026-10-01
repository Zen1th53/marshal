# Configuration

MARSHAL initializes missing project policy/version defaults with `marshal init`.
Inspect policy with `/policy` and sandbox mode with `/sandbox`.
Native provider sessions keep the provider's own configuration and sign-in.

## Marshal settings

Use `/marshal settings` to inspect settings. Changes apply to the next run.

| Key | Values | Default |
| --- | --- | --- |
| `execution-rights` | `none`, `read-only`, `small-tasks` | `read-only` |
| `acceptance-mode` | `marshal`, `marshal-then-user`, `user` | `marshal-then-user` |
| `control` | `free`, `strict` | `free` |
| `rework-limit` | Non-negative rework count | `2` |
| `ultra-concurrency` | Positive worker count | `3` |

Example: `/marshal settings rework-limit 2`. Execution rights bound work the
Marshal may perform itself. They do not replace worker approvals.
See [Marshal mode](marshal.md) for acceptance and control.

## Terminal options

```bash
marshal tui work --theme high-contrast --no-animation
```

The positional `session-id` chooses a session (default `default-session`). `--theme <name>` accepts
`default`, `monochrome`, `high-contrast` and `no-color`. `--no-color` disables
terminal colors; `--no-animation` disables animations.

## User-facing environment variables

| Variable | Purpose |
| --- | --- |
| `MARSHAL_NO_UPDATE_CHECK=1` | Disable contact with the public release feed for update checks. |
| `MARSHAL_PROVIDER_PATH_ONLY=1` | Discover provider binaries through `PATH` only. |
| `CODEX_HOME` | Existing native Codex configuration location. |
| `CLAUDE_CONFIG_DIR` | Existing native Claude configuration location. |

Example: `MARSHAL_NO_UPDATE_CHECK=1 marshal tui`.
Use each provider's own interface for authentication. ULTRA execution has no
environment-variable switch. Model/effort preferences affect future governed
runs; native sessions retain their own settings.
