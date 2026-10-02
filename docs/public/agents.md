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

## Choose your main agent

If you have more than one agent installed, tell MARSHAL which one to use for
everyday commands such as `/models`, `/mcp` and `/resume`:

```text
/provider use claude
```

You can use `codex`, `claude`, `opencode` or `agy`. MARSHAL remembers your
choice for this project. If only one agent is installed, MARSHAL uses it
without asking.

When your main agent does not have a command, MARSHAL tells you which agents
do. It never quietly uses a different agent instead.

## Check that MARSHAL can see your agents

In a terminal:

```bash
marshal doctor --probe-providers
```

Or inside MARSHAL:

```text
/provider status
```

This shows which agents MARSHAL found and which one is your main agent. It
cannot tell whether you are signed in. To sign in, use `/login`, or open the
agent on its own (for example run `claude`) and sign in there.

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

To see your main agent's models:

```text
/models
```

To see another agent's models, name it, for example `/models opencode`. If
you have not chosen a main agent yet, `/models` shows every installed agent.

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
