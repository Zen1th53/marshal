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

## Choose the Marshal provider

Use `/marshal model claude` to switch the Marshal to Claude Code, or choose
`codex` or `agy`. MARSHAL checks that the CLI is installed before closing the
current chat. A different provider replaces that chat immediately through the
normal startup path with hidden instructions; the window name and control
centre focus are kept. It says, for example, “The Marshal now uses claude.”
Choosing the same provider leaves the chat running.

The choice is saved as this project's default provider, also used by everyday
commands such as `/models`, `/mcp` and `/resume`. Startup, reattachment and
recovery keep the chosen Marshal provider. `/marshal chat` reuses its matching
chat. The Marshal chat is never stopped automatically or by stopping workers;
only an explicit `/marshal model` switch or closing its window replaces it.

## Marshal chat instructions

The Marshal receives its planning protocol through hidden instructions. The
visible opening is a short kickoff asking it to begin and confirm your language.
The protocol is delivered again when the chat resumes or restarts, even if memory
injection is disabled. If hidden delivery fails or is unavailable, MARSHAL refuses
to start the Marshal and shows a failure message. It never pastes the protocol
into the chat. Historical copies are withheld from memory, peer briefings,
workspace output and evidence views.

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
These native workers are trusted and run with your user account's rights.
MARSHAL does not sandbox them. Governed workers are sandboxed; opening an
agent's native interface does not give it that protection.

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
