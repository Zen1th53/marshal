<div align="center">

# Marshal

### Plan with one AI. Let other AIs build it. Stay in charge.

**Marshal is a free, open-source orchestrator for the coding agents you already use.
Claude Code, Codex, OpenCode and Antigravity work as one team in your terminal:
one plans with you, others build in isolated worktrees, a different one reviews,
and only verified work lands on `main`.**

[![CI](https://github.com/Zen1th53/marshal/actions/workflows/ci.yml/badge.svg)](https://github.com/Zen1th53/marshal/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/Zen1th53/marshal?color=blue)](https://github.com/Zen1th53/marshal/releases)
[![Go Version](https://img.shields.io/github/go-mod/go-version/Zen1th53/marshal)](https://go.dev)
[![License: AGPL v3](https://img.shields.io/badge/License-AGPL--3.0--only-blue.svg)](LICENSE)
[![Platform](https://img.shields.io/badge/platform-Linux%20%7C%20macOS%20%7C%20Windows%20(WSL)-informational)](#requirements)
[![Status: early release](https://img.shields.io/badge/status-early%20release-orange.svg)](https://marshal.blackhat.uz)

**[Documentation → marshal.blackhat.uz/docs](https://marshal.blackhat.uz/docs)**

<p align="center">
  <img src=".github/assets/demo.gif" alt="The Marshal Board v2 from launch to a finished run: the Who plans? picker with each provider's limit, the readiness checklist with the Marshal, workers, platform, repository and browser, a goal said from the Board, the planning screen with the Marshal's steps, the Accept architecture card over the dimmed Board with the architecture accepted in the browser, then the running control centre with the top bar and NOW line, the Plan, Lanes and Tasks panes, the Marshal, Workers, Inbox and Activity panes, each task reviewed by a different provider, verified and merged." width="100%">
  <br>
  <sub>The Board's own rendering on fixture data, sped up: <b>Who plans?</b> → readiness → say the goal → planning → <b>Accept architecture</b> card, accept in the browser → lanes build in parallel → each hand-in reviewed by a different provider → verify → merge</sub>
</p>

[Set up](#set-up-with-your-agent) ·
[How it works](#how-it-works) ·
[Why Marshal](#why-marshal) ·
[The Board](#the-board) ·
[FAQ](docs/faq.md) ·
[Docs](#documentation)

</div>

## Set up with your agent

Paste this into Claude Code or Codex. It installs Marshal, checks Git, tmux and your coding
agents, fixes what is safe to fix, and tells you what is left. It does not create a project
and does not touch your repositories.

<!-- setup-prompt:start -->
````text
Set up Marshal (https://github.com/Zen1th53/marshal) on this computer. Marshal is a terminal tool that runs coding agents (Claude Code, Codex, OpenCode, Antigravity) as one team. Install it, check that everything it needs is present, fix what is safe to fix, and tell me what is left.

Rules for this job:
- Do not create a test project. Do not run `marshal init`, `marshal doctor` or `marshal` itself, and do not change any Git repository.
- Never use sudo or install system packages without first showing me the exact command and getting my yes.
- Do not sign in to anything for me, and do not read, print or copy credentials or tokens.
- Do not run `claude auth status`: it rewrites Claude Code's settings file. Use only the read-only checks below.
- Run every check from a new empty folder: cd "$(mktemp -d)"
- If your sandbox blocks network access or installing into my home folder, ask me to allow it for this job.

1. Platform. Run `uname -s`, `uname -m` and `uname -r`.
   - Supported: Linux or macOS (Darwin) on x86_64/amd64 or arm64/aarch64.
   - If `uname -r` contains "microsoft" (any case), this is Windows WSL. It must be WSL2 ("WSL2" in `uname -r`). Everything below happens inside this Linux system; tools installed on the Windows side do not count.
   - Anything else (plain Windows, another CPU): stop and tell me there is no Marshal release for it.

2. Install and set up. Run exactly:
   curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
   It downloads the release for this machine, verifies its checksum, installs `marshal` into ~/.local/bin (or $MARSHAL_INSTALL_DIR if I set it), adds that folder to PATH in my shell's startup file, checks Git and tmux (installing them with Homebrew on macOS), lists the coding agents it finds and registers the anonymous install count. It never runs sudo without a terminal to answer. If it fails, show me its last lines and stop.

3. Left to do. If the installer printed "Left to do:", go through it:
   - A PATH line: run it in this session (export PATH="$HOME/.local/bin:$PATH").
   - A sudo command (Git or tmux on Linux): show it to me and run it only after I say yes. Check `git --version` / `tmux -V` again afterwards.
   - macOS without Homebrew and no Git: ask me to run `xcode-select --install` (it opens a dialog).
   - "too old" tmux that the package manager cannot upgrade (Ubuntu 22.04 ships 3.2a): do not build it from source. Tell me I need Ubuntu 24.04 or newer, Debian 12 or newer, or a tmux built from source.
   Then `marshal version` must print a line that starts with "Marshal v".

4. Readiness. Run `marshal setup status`. It only reports and changes nothing. In this empty folder "No project is open" and "This directory is not part of a Git repository" are expected; ignore them. Tell me about any other line whose status is not READY or OPTIONAL.

5. Coding agents. For each of claude, codex, opencode and agy: run `command -v <name>` and, if found, `<name> --version`. Marshal needs at least one; with two or more, a different model reviews each result. Do not install any of them unless I ask.
   Sign-in, read-only only:
   - codex: run `codex login status`.
   - claude, opencode, agy: there is no read-only check. Report "installed, sign-in not checked" and tell me how to sign in: Claude Code: run `claude` and use /login. OpenCode: run `opencode auth login`. Antigravity: sign in inside the Antigravity app.

6. Telemetry. Run `marshal telemetry status`. Tell me in one line: Marshal sends anonymous usage telemetry (a random install ID, the version, OS and CPU type, and timestamps; never code, prompts or file paths), and `marshal telemetry disable` turns it off. Do not change it yourself.

7. Report. Finish with this short report and nothing after it:
   Marshal: <version> at <path> (PATH: ok | added to <file> | needs fixing)
   Platform: <Linux | macOS | WSL2> <cpu>
   Git: <version | missing>
   tmux: <version> (<ok | too old | missing>)
   Agents: <name: version, signed in | not signed in | sign-in not checked>, one per installed agent; "none found" if none
   Fixed: <what you changed, or "nothing">
   Needs you: <one line per remaining step, or "nothing">
   Telemetry: <on | off> (marshal telemetry disable turns it off)
   Next, check the installed version with `marshal version`, then open a Git repository with `cd /path/to/your/repository && marshal`.
````
<!-- setup-prompt:end -->

Or install it yourself (Linux, macOS, Windows via WSL2; checksum-verified, no sudo):

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

Then, in a Git repository of yours, run `marshal`. Choose who plans, check the readiness
list (sign in to any provider it marks), and say what you want built with `s` or in the
Marshal chat. Confirm the requirements card with `Enter` (or edit an answer with `e`); when the
**Accept architecture** card opens, press `a` to open the architecture in your browser and accept
it there. That is the owner gate. The Marshal then plans and approves the plan itself, starts the
workers, and runs a final independent check. You need at least one coding agent CLI, installed and
signed in ([requirements](#requirements)); Marshal has no model costs of its own.

**Telemetry:** Marshal sends anonymous usage telemetry (a random install ID, version,
OS/architecture, mode and timestamps; never code, prompts, paths or commands).
`marshal telemetry disable` turns it off. [Exactly what is sent →](docs/telemetry.md)

---

## How it works

```text
  you ──goal──▶ MARSHAL ──architecture──▶ you accept (in the browser)
                   │
                   ▼
                 plan: small tasks, each with files, criteria and a check
                   │
        ┌──────────┼──────────┐
        ▼          ▼          ▼
     worker     worker     worker        each in its own git worktree
    (codex)    (claude)   (opencode)
        └──────────┼──────────┘
                   ▼
     review by a different provider  ──▶  independent verifier runs the checks
                   │
                   ▼
            merged and closed to main
```

1. **Plan together.** One agent is the *Marshal*. It reads the repository,
   asks only questions that change the plan, and drafts an architecture you
   accept or send back.
2. **Build in isolation.** The plan is split into small tasks. Each task names
   its files, acceptance criteria and a check command, and runs in its own Git
   worktree and branch, so parallel workers never overwrite each other.
3. **No model grades its own work.** A different provider reviews every
   hand-in against its criteria. An independent verifier then runs the checks
   on the integrated result.
4. **Only verified work lands.** Accepted tasks merge, and the run closes to
   `main` with a record of who did what and which checks passed.

[Full lifecycle →](docs/how-it-works.md)

---

## Why Marshal

Coding agents are good at writing code and bad at knowing when they are wrong.
With one agent, the same model plans, writes, tests and approves its own work.
With several agents, you become the glue: copying context between terminals,
keeping them out of each other's files and checking every claim yourself.

Marshal turns that into a process:

- **One conversation, a whole team.** You talk to one Marshal. It plans,
  dispatches and checks; you make the decisions that matter.
- **Cross-provider review.** Claude reviews Codex, Codex reviews Claude.
  No model reviews its own work.
- **A separate worktree per task.** A worktree and branch per task, a declared
  file scope, and a hand-in with commit digests and check results.
- **Live and honest state.** The Board shows tasks, lanes, decisions and
  activity as they happen. Nothing is marked done without its check.
- **Shared memory.** What one agent learns about your project is recorded and
  reviewed, and every later agent can use it (local MCP server included).
- **Your tools, your accounts.** Marshal drives the official CLIs you already
  have. It is a single Go binary with state in `.marshal/` inside your repo.

---

## The Board

`marshal` opens a tmux workspace: the Marshal chat docks on the left, the Board
on the right. The Board is a control centre of titled panes:

```text
  MARSHAL v0.3.5   project marshal-work / run review-fixes   RUNNING     ✳ claude · opus-5.5 · high  m   8 workers  inbox 0  19:31
 NOW  marshal claude Opus · watching reviews    │ work 8 running · 4 queued           from you nothing · next ask: final review

╭─ PLAN · 12 tasks · 18 requirements ────────────────────── accepted rev 1 · 18:52 ─╮ ╭─ MARSHAL ─────────────────────── m switch ─╮
│ ✓ goal → ✓ reqs → ✓ design → ✓ arch → ✓ tasks →  build 5/12  → review → merge     │ │ ✳ claude  opus-5.5 · effort high    in use │
╰───────────────────────────────────────────────────────────────────────────────────╯ ╰────────────────────────────────────────────╯
╭─ LANES · from worker heartbeats ────────────── limit claude 4 · agy 4 · total 12 ─╮ ╭─ WORKERS · providers ──────────────────────╮
│  ⣾ claude-1    sonnet-5.5   T03 sub-view external links   tests        14m     8s │ │ ✳ claude  ██████░░░░   4 lanes  sonnet     │
│  ◎ agy-3       gemini-3.8   review T01 (by claude-2)      reviewing     3m     2s │ │ ◆ agy     ███████░░░   4 lanes  gemini-3.8 │
╰───────────────────────────────────────────────────────────────────────────────────╯ ╰────────────────────────────────────────────╯
╭─ TASKS · 5 of 12 merged ───────────────────────────────────── ⏎ open · r restart ─╮ ╭─ INBOX ────────────────────────── i opens ─╮
│   T01  stale target refusal message  review.go     claude  agy     ✓ merged       │ │ Nothing waits for you.                     │
│   T03  sub-view external links       review.html   claude  agy     › building     │ ╰────────────────────────────────────────────╯
╰───────────────────────────────────────────────────────────────────────────────────╯ ╭─ ACTIVITY · grouped ───────────────────────╮
```

- **Top bar**: project and run, the state (`READY`, `PLANNING`, `YOUR TURN`, `RUNNING`, `PAUSED` …),
  and the Marshal segment with its provider, model and effort (`m` changes it).
- **NOW line**: what the Marshal and the workers are doing, and **from you**: what waits for
  you, or `nothing` and the next thing you will be asked. It is never empty.
- **Plan, Lanes, Tasks** on the left: stages and progress, one row per worker from its
  heartbeats, and every task with its writer, reviewer and state.
- **Marshal, Workers, Inbox, Activity** on the right: the Marshal's model, context and chat
  binding, each provider's limit and lanes, what waits for you, and what happened.
- **Cards** for your decisions: the **Accept architecture** card (open it in the browser with
  `a`; when no browser can open, the card shows the link), plan and inbox decisions, and the
  **paused** card that says what stopped, what is kept, and what `Enter` does to continue.
- **MARSHAL CHAT** strip: the docked chat's title shows its model, whether it is bound to this
  run, and whether its input is clear (`input clear` / `input waiting`).

| Key | Action |
|---|---|
| `s` | Say something to the Marshal from the Board |
| `Enter` / `F11` | Open the Marshal chat; `F11` switches between chat and the Board |
| `a` | Open the architecture in your browser (where you accept it) |
| `r` | Restart a stuck task; `Enter` confirms |
| `m` | Change the Marshal's model or provider |
| `Space` or `Ctrl+K` | Action palette: say, architecture, restart, model, pause everything (`p`), stop all workers, limits and workers (`l`), help |
| `Ctrl+X` | Stop all workers (never the Marshal) |
| `?` | Every key |

Select a task and press `Enter` to inspect it; press `o` in the inspector to open its worker pane, or `w` to change its model. The final ULTRA check requires Claude Code installed and not used as a worker in that run; if unavailable, the run stops before checking and tells you to install Claude Code or wait for its limit, then press `Enter`. `MARSHAL_BOARD=classic` keeps the classic Board for one release. [All screens and keys →](docs/board-and-keys.md)

---

## Requirements

- Linux or macOS (amd64 or Apple Silicon/arm64). On Windows, use WSL2: run
  the installer inside your WSL2 Linux distribution.
- Governed workers (sandboxed, socket-confined) run on **Linux amd64** only.
  On macOS and Linux arm64 Marshal plans, reviews and shows the architecture,
  and says plainly that it cannot run the governed workers there.
- Git and tmux 3.3a or newer
- At least one provider CLI, installed and signed in:
  `claude` (Claude Code), `codex` (OpenAI Codex), `opencode` (OpenCode) or
  `agy` (Antigravity). Two or more providers enable cross-provider review.

Check your setup with `marshal doctor`, or press `v` on the Board.

Build from source instead:

```bash
git clone https://github.com/Zen1th53/marshal.git && cd marshal
go build -o marshal ./cmd/marshal && install -Dm755 marshal ~/.local/bin/marshal
```

> [!WARNING]
> **Workers run on your machine, as you.** Worktrees isolate Git changes, not
> processes: workers have your user's file and network access. Use a dedicated
> project directory and review the architecture before you accept it; the
> Marshal approves the plan itself. Marshal refuses `/` and your home
> directory as a project root.

---

## Questions

How Marshal differs from running one agent directly, what it costs, which platforms it runs on
and how ready it is: [FAQ →](docs/faq.md)

---

## Documentation

- [Set up with your agent](docs/SETUP-PROMPT.md) · [Installation](docs/installation.md) · [Getting started](docs/getting-started.md) · [How a run works](docs/how-it-works.md)
- [The Board and keys](docs/board-and-keys.md) · [Providers](docs/providers.md) · [Memory and MCP](docs/memory-and-mcp.md)
- [Configuration](docs/configuration.md) · [Telemetry](docs/telemetry.md) · [FAQ](docs/faq.md) · [Troubleshooting](docs/troubleshooting.md)
- [Upgrading from v0.0.x](docs/upgrading.md) · [Changelog](docs/CHANGELOG.md)

## Contributing

Issues and pull requests are welcome. Read [CONTRIBUTING](.github/CONTRIBUTING.md)
first. Contributions require the contributor agreement described there.

## License

Marshal is licensed under the GNU Affero General Public License v3.0
(AGPL-3.0-only), with commercial licences available. See
[LICENSING.md](docs/legal/LICENSING.md) and
[COMMERCIAL-LICENSING.md](docs/legal/COMMERCIAL-LICENSING.md). Bundled
open-source components are listed in
[docs/legal/THIRD_PARTY_NOTICES.md](docs/legal/THIRD_PARTY_NOTICES.md).
