# Settings

## How the Marshal runs work

See your current settings:

```text
/marshal settings
```

Change one:

```text
/marshal settings control strict
```

Changes apply to the **next** plan, not the one already running.

| Setting | What it does | Choices | Default |
| --- | --- | --- | --- |
| `control` | How closely agents follow the task notes. `strict`: exactly as written. `free`: agents choose their own approach, within the task's files or directories. | `free`, `strict` | `free` |
| `acceptance-mode` | Who decides that a task is finished. | `marshal`, `user`, `marshal-then-user` | `marshal-then-user` |
| `execution-rights` | What the Marshal may do itself, apart from the agents. | `none`, `read-only`, `small-tasks` | `read-only` |
| `rework-limit` | How many times a task can be sent back to the same agent. After that, MARSHAL gives the task to a different agent, or brings it to you if another agent has already tried. | a number | `2` |
| `ultra-concurrency` | How many agents can work at once with ULTRA. | a number | `3` |

**Acceptance modes in plain words:**

- `marshal`: the Marshal reviews and accepts tasks.
- `user`: you review and accept every task.
- `marshal-then-user`: the Marshal reviews first, then you decide. This is the
  default and the safest choice to start with.

!!! note
    `strict` is an instruction to the agents, not a guarantee. Always review
    the result.

## Window appearance

You can start MARSHAL with a different look:

```bash
marshal tui --theme high-contrast
marshal tui --no-animation
```

Themes: `default`, `monochrome`, `high-contrast`, `no-color`.

## Environment variables

| Variable | What it does |
| --- | --- |
| `MARSHAL_NO_UPDATE_CHECK=1` | Do not check online for new versions. |
| `MARSHAL_PROVIDER_PATH_ONLY=1` | Look for agents only in the folders on your `PATH`. |
| `MARSHAL_VERSION` | Used by the installer to pick a version. |
| `MARSHAL_INSTALL_DIR` | Used by the installer to choose where to put `marshal`. |

Example:

```bash
MARSHAL_NO_UPDATE_CHECK=1 marshal tui
```
