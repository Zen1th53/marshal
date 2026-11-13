# Marshal

Marshal is a free, open-source (AGPL-3.0 plus a commercial licence) local multi-agent coding orchestrator. Plan work with one AI, let other AI agents do it, and stay in charge.

Instead of typing a request into one AI assistant and hoping it makes no mistakes, Marshal introduces a structured multi-agent workflow: one model acts as the **Marshal** to clarify your goals, draft architecture, and plan discrete tasks; other AI agents execute those tasks across isolated branches; a cross-provider reviewer evaluates the code; and an independent verifier tests the integrated result before it is merged into `main`.

> [!WARNING]
> **Important Notice on Worker Execution**: Worker agents run directly on your host machine with your user permissions and without a sandbox. Worktrees isolate Git changes, not system calls. The Marshal plans and approves the work itself, and workers run without pausing for per-command approval; your one gate is accepting the architecture, so review it before you accept, watch the run on the Board, and stop it at any time with `Ctrl+X`.

---

## 60-Second Quick Start

Get from zero to an executed multi-agent run in four steps:

```bash
# 1. Install Marshal to ~/.local/bin
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh

# 2. Enter a Git repository and open Marshal
cd /path/to/project
marshal
```

Once inside the Board:
1. **Confirm requirements**: The Marshal fills a requirements card. Press `Enter` to confirm all, or `e` to edit an answer.
2. **Accept the architecture**: Press `y` to open the architecture in your browser and accept it. This is the owner gate.
3. **The Marshal plans**: The Marshal creates and approves the plan itself; there is no plan approval popup. Tasks run, then a final independent check reruns the checks. The final ULTRA check needs Claude Code installed and unused as a worker in that run.
4. **Finish**: The run closes to `main`, and the chat says `[Marshal] The run closed: …`.

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
- [Getting Started](getting-started.md) — End-to-end walkthrough from opening a project to a completed run.
- [The Board and Keys](board-and-keys.md) — Screen layout, focus switching, shortcuts, and `NEEDS YOU` gates.
- [Providers](providers.md) — Configuring Claude Code, Codex, OpenCode, and Antigravity, plus harness verification.
- [How a Run Works](how-it-works.md) — Planning, workers, cross-provider review, independent verification, and closing to `main`.
- [Memory and MCP](memory-and-mcp.md) — Using the MCP server, memory commands, and candidate review.
- [Configuration](configuration.md) — TUI settings, project files, environment variables, and CLI options.
- [Telemetry](telemetry.md) — Anonymous telemetry details and how to opt out.
- [Troubleshooting](troubleshooting.md) — Solutions for uninitialized projects, provider limits, and resuming runs.
- [Changelog](CHANGELOG.md) — Release notes and history.
