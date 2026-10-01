# Connect your AI agents

MARSHAL does not include an AI of its own. It works with the AI coding agents
you already use. Install at least one, and sign in with its own login.

| Agent | Command | Can be the Marshal | Tested version |
| --- | --- | --- | --- |
| Codex | `codex` | Yes | 0.159.2 |
| Claude Code | `claude` | Yes | 2.1.286 |
| Antigravity | `agy` | Yes | 1.2.7 |
| OpenCode | `opencode` | No, worker only | 1.18.16 |

Other versions may also work. MARSHAL has checked its commands only against
the versions above, and labels commands for other versions as not checked.

!!! note "Antigravity"
    The Antigravity desktop app alone is not enough. MARSHAL needs its
    command-line tool, `agy`.

## Check that MARSHAL can see your agents

In a terminal:

```bash
marshal doctor --probe-providers
```

Or inside MARSHAL:

```text
/provider status
```

This shows which agents MARSHAL found. It cannot tell whether you are signed
in. If an agent does not work, open it on its own (for example run `codex`)
and sign in there.

## Use an agent directly

You can open any agent's own interface from inside MARSHAL:

```text
/codex
/claude
/opencode
/agy
```

Leave the agent the usual way to come back to MARSHAL. When used like this,
the agent works exactly as it does on its own, with its own settings.

To continue your most recent conversation in this project:

```text
/codex resume --last
/claude continue
```

See [Continue past conversations](sessions.md) for more.

## Choose a model

To see the Codex models MARSHAL found:

```text
/models
```

To choose the model for future planned work:

```text
/model select codex MODEL_NAME
/model select claude MODEL_NAME
```

Replace `MODEL_NAME` with a name from the list. Work that has already started
keeps its model.

For Codex you can also choose how hard it thinks:

```text
/effort high
/effort default
```

The levels are `minimal`, `low`, `medium`, `high` and `xhigh`. This setting
applies to Codex only.

## Keep your credentials safe

Sign in through each agent's own login. Never type passwords or API keys into
the MARSHAL composer or into the plan files.
