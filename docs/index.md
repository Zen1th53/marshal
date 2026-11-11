# Marshal

Marshal is a free, open-source (AGPL-3.0 plus a commercial licence) local multi-agent coding orchestrator. Plan work with one AI, let other AI agents do it, and stay in charge.

Instead of typing a request into one AI assistant and hoping it makes no mistakes, Marshal introduces a structured multi-agent workflow: one model acts as the **Marshal** to clarify your goals, draft architecture, and plan discrete tasks; other AI agents execute those tasks across isolated branches; a cross-provider reviewer evaluates the code; and an independent verifier tests the integrated result before it is merged into `main`.

> [!WARNING]
> **Important Notice on Worker Execution**: Worker agents run directly on your host machine with your user permissions and without a sandbox. Worktrees isolate Git changes, not system calls. Always review plans, commands, and code changes before approving them.

---

## 60-Second Quick Start

Get from zero to an executed multi-agent run in four steps:

```bash
# 1. Install Marshal to ~/.local/bin
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh

# 2. Enter a Git repository and initialize Marshal
cd /path/to/project
marshal init

# 3. Launch the interactive Board
marshal
```

Once inside the Board:
1. **State your goal**: Press `s` to type your request directly to the Marshal, or focus the chat pane with `F11` or `Alt+←`.
2. **Accept the architecture**: The Marshal clarifies requirements and drafts a project contract. When the `NEEDS YOU` gate appears, press `y` to open the architecture in your browser and click **Accept architecture**; the run then moves on by itself.
3. **The Marshal plans**: The Marshal decomposes your goal into discrete tasks with verifiable criteria and approves the plan itself. You can stop the run at any time with `Ctrl+X`.
4. **Watch the run**: Worker agents execute tasks in parallel branches, a cross-provider reviewer inspects the diffs, an independent verifier runs checks, and accepted changes are closed to `main`.

---

## Key Features

- **Local & Multi-Agent**: Orchestrate Claude Code, OpenAI Codex, OpenCode, and Google Antigravity (`agy`) simultaneously on your own machine.
- **The Board (TUI)**: A dynamic tmux-powered terminal dashboard providing real-time visibility into tasks, worker lanes, activity logs, and a docked conversation pane.
- **Cross-Provider Review**: No agent marks its own homework. Changes handed in by one provider are reviewed against acceptance criteria by a different provider.
- **Independent Verification**: Test suites and acceptance criteria are evaluated by an independent verifier before code is integrated.
- **Project Memory & MCP**: Built-in Model Context Protocol server and persistent candidate-reviewed knowledge store.
- **Transparent & Anonymous**: Anonymous opt-out telemetry directed to `marshal.blackhat.uz`, with no code, prompts, or sensitive data transmitted.

---

## Documentation Guide

- [Installation](installation.md) — System requirements, `install.sh`, building from source, and setup verification.
- [Getting Started](getting-started.md) — End-to-end walkthrough from `marshal init` to completed run.
- [The Board and Keys](board-and-keys.md) — Screen layout, focus switching, shortcuts, inspector action row, and `NEEDS YOU` gates.
- [Providers](providers.md) — Configuring Claude Code, Codex, OpenCode, and Antigravity, plus harness verification.
- [How a Run Works](how-it-works.md) — Planning, workers, cross-provider review, independent verification, and closing to `main`.
- [Memory and MCP](memory-and-mcp.md) — Using the MCP server, memory commands, and candidate review.
- [Configuration](configuration.md) — TUI settings, project files, environment variables, and CLI options.
- [Telemetry](telemetry.md) — Anonymous telemetry details and how to opt out.
- [Troubleshooting](troubleshooting.md) — Solutions for uninitialized projects, provider limits, and resuming runs.
- [Changelog](CHANGELOG.md) — Version 0.2.0 release notes and history.
