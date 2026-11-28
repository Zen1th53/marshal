<div align="center">

# Marshal

### Plan with one AI. Let other AIs build it. Stay in charge.

**Marshal is a free, open-source orchestrator for the coding agents you already use.
Claude Code, Codex, OpenCode and Antigravity work as one team: one plans with you,
others build in parallel Git worktrees, every hand-in is checked before it merges,
and you follow and steer it all from a local Web Board.**

[![CI](https://github.com/Zen1th53/marshal/actions/workflows/ci.yml/badge.svg)](https://github.com/Zen1th53/marshal/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/Zen1th53/marshal?color=blue)](https://github.com/Zen1th53/marshal/releases)
[![Go Version](https://img.shields.io/github/go-mod/go-version/Zen1th53/marshal)](https://go.dev)
[![License: AGPL v3](https://img.shields.io/badge/License-AGPL--3.0--only-blue.svg)](LICENSE)
[![Platform](https://img.shields.io/badge/platform-Linux%20%7C%20macOS%20%7C%20Windows%20(WSL)-informational)](#requirements)
[![Status: early release](https://img.shields.io/badge/status-early%20release-orange.svg)](https://marshal.blackhat.uz)

**[Documentation → marshal.blackhat.uz/docs](https://marshal.blackhat.uz/docs)**

<p align="center">
  <img src=".github/assets/web-work.png" alt="The Marshal Web Board's Work section during a real run on a Mac: the stages goal, requirements, design, architecture and tasks are done and build is in progress; four tasks (color, done, due, export) are building in parallel on codex and claude workers, with a live table of workers, their model, step, running time and last signal." width="100%">
  <br>
  <sub>The Web Board during a real run on a Mac: four tasks build in parallel on Codex and Claude Code workers.</sub>
</p>

<table>
  <tr>
    <td width="40%"><img src=".github/assets/web-team.png" alt="The Team section: each provider's role, model and limits per model group and window, such as codex 84% of its 5-hour window left and agy's Claude and GPT group at 0% until Nov 26." width="100%"><br><sub><b>Team</b>: who does what, and each provider's limits per model group and window.</sub></td>
    <td width="40%"><img src=".github/assets/web-architecture.png" alt="The architecture review page: the proposed parts (cli, store, format, export, task) grouped by area with their links, and the Accept architecture button." width="100%"><br><sub><b>Architecture</b>: the Marshal proposes, only you accept.</sub></td>
    <td width="20%"><img src=".github/assets/web-phone.png" alt="The Work section on a narrow screen with bottom navigation: Home, Work, Arch, Team, Activity." width="100%"><br><sub>Narrow screens get their own layout.</sub></td>
  </tr>
</table>

[What you get](#what-you-get) ·
[Set up](#set-up-with-your-agent) ·
[How it works](#how-it-works) ·
[Why Marshal](#why-marshal) ·
[The Board](#the-board) ·
[FAQ](docs/faq.md) ·
[Docs](#documentation)

</div>

## What you get

- **Agree before anything is built.** The Marshal turns your goal into a requirements card you
  confirm, then proposes the project's architecture, which only you can accept in the browser.
- **Parallel workers.** Codex, Claude Code and the others build separate tasks at the same time,
  each in its own Git worktree and branch.
- **Architecture carried into the work.** In the default edition, workers receive their parts of
  the accepted architecture. Hand-ins go back with file locations for code outside those parts,
  new source outside every part, or undeclared Go imports when the architecture declares dependencies.
  Edits to existing unowned files are flagged, not returned.
- **Checked before it merges.** Every hand-in is reviewed by the Marshal and its task's checks run
  before it merges. On every plan, Standard and ULTRA, each hand-in also gets an independent
  review, and a final independent check reruns the checks on the integrated result: by another
  provider when one is free, otherwise by a fresh session of the same one.
- **A Web Board as the cockpit.** A local page (127.0.0.1, opened with a one-use link) shows Home,
  Work, Architecture, Team, Activity and Settings, and asks you only when a decision is yours.
- **Limits you can see.** Each provider's limits per model group and window, with reset times;
  when one group is spent, the others keep working.
- **A short terminal home.** One screen in the terminal (state, what is happening, what is needed
  from you), with the Marshal chat one key away.

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

The installer counts the install anonymously. To install without being counted, run the installer with telemetry off: `curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | MARSHAL_CLOUD_ENDPOINT=off sh`. `marshal telemetry disable` keeps it off afterwards.

Then, in a Git repository of yours, run `marshal`. The first time, it shows what it will commit
(three small config files, on your current branch) and how many uncommitted files workers will
not see, and asks before it changes anything. Choose who plans, check the readiness
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
     review in a fresh agent session ──▶  independent verifier runs the checks
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
3. **Checked before it merges.** By default, plans get independent review
   and a final check. In the free build, review can be Off. An approved
   checks-only policy runs executable checks without an independent final
   model check; its delivery is labelled checks-only, not independently verified.
   The final model check currently requires Claude Code installed and signed in.
   With review enabled, another agent with a fresh context
   reviews every hand-in against its criteria: a different provider when one
   is available, otherwise a new session of the same one. When final model
   checking is enabled, an independent verifier in a session of its own runs the checks on the
   integrated result.
4. **Delivery records the effective guarantee.** Accepted tasks merge, and the
   run closes to `main` with a record of who did what and which checks passed.

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
- **Independent review.** Another provider reviews the work when one is free;
  otherwise a fresh session of the same one, which never sees the worker's conversation.
- **A separate worktree per task.** A worktree and branch per task, a declared
  file scope, and a hand-in with commit digests and check results.
- **Live and honest state.** The Board shows tasks, lanes, decisions and
  activity as they happen. Nothing is marked done without its check.
- **Shared memory.** Design artifacts, session imports, task outcomes and explicit memory notes are recorded
  and reviewed (kept or rejected). Later workers receive relevant durable or verified records
  within their scope, freshness and context limits; raw session history is excluded. Recall is also
  available through the local MCP server (`marshal mcp serve`).
- **Your tools, your accounts.** Marshal drives the official CLIs you already
  have. It is a single Go binary with state in `.marshal/` inside your repo.

---

## The Board

<p align="center">
  <img src=".github/assets/demo.gif" alt="A real Marshal run on a Mac in the terminal, sped up: Claude Code is picked as the Marshal, the goal is said from the Board, the Marshal asks one question and fills the requirements card, the requirements are confirmed, the architecture is accepted in the browser, Codex builds four tasks in parallel, each task is reviewed and merged, and the run ends DONE with all four on main." width="100%">
  <br>
  <sub>The terminal side of a real run on a Mac, sped up: say the goal → confirm the requirements → accept the architecture in the browser → four tasks build in parallel → reviewed and merged → DONE.</sub>
</p>

`marshal` opens a tmux workspace: the Marshal chat docks on the left, the Board
on the right. The terminal Board is one short screen; the **Web Board** is the
full interface, and it opens in your browser as the Board starts:

```text
 MARSHAL · marshal-work · RUNNING · 8 workers · 19:31

 Now:       8 running · 4 queued (5/12 done)
 From you:  nothing
 Web:       opened in your browser (w opens it again)
 ⏎ chat   w web   s say to the Marshal   q quit
```

- **Now**: what is happening, in one sentence.
- **From you**: `nothing`, or the one thing that needs you. A safe step (retry a stuck task,
  move it and continue, resume, confirm the requirements) is named with its key, `y`. A
  decision with authority (allow or deny a request, a gate, the architecture, stopping the
  run) says `open the web to decide (w)`. When the Marshal asked you something in its chat,
  it says so: `Enter` opens the chat.
- **Web**: where the Web Board is. Its private link is never on screen: `/marshal web` (or
  `w` on a computer with no display) shows it. `MARSHAL_WEB_OPEN=0` keeps the browser closed
  at start.
- The Web Board shows the plan, lanes, tasks, workers, activity, the Marshal's steps and
  requests, the readiness checklist and every card with its buttons.

| Key | Action |
|---|---|
| `Enter` / `F11` | Open the Marshal chat; `F11` switches between chat and the Board |
| `y` | Do the safe step **From you** names; a second `y` within 2 s does nothing |
| `w` | Open the Web Board |
| `s` | Say something to the Marshal from the Board |
| `a` | Open the architecture in your browser (where you accept it) |
| `m` | Change the Marshal's model or provider |
| `Space` or `Ctrl+K` | Action palette |
| `Ctrl+X` | Stop all workers (never the Marshal) |
| `q` | Quit |
| `?` | Every key |

The views (`2`–`8`) still open over the Board. `/task-model <id>` changes the model a task
runs on. When the final model check is required, it needs Claude Code installed and signed
in. It runs in a fresh session, even if Claude Code also worked on the run. If unavailable, the run stops
before checking and tells you to install Claude Code or wait for its limit. An approved
checks-only policy skips this model check and labels the delivery checks-only. [All screens and keys →](docs/board-and-keys.md)

---

## Requirements

- Linux or macOS (amd64 or Apple Silicon/arm64). On Windows, use WSL2: run
  the installer inside your WSL2 Linux distribution.
- In the standard release, workers run on your host in Git worktrees on Linux
  and macOS (amd64/arm64); Antigravity (`agy`) workers run on Linux only.
  Sandboxed, socket-confined workers require `-tags governed` and Linux amd64.
- Git and tmux 3.3a or newer
- At least one provider CLI, installed and signed in:
  `claude` (Claude Code), `codex` (OpenAI Codex), `opencode` (OpenCode) or
  `agy` (Antigravity). Two or more providers enable cross-provider review.

Check your setup with `marshal doctor`. To verify a provider with a model call,
open the Providers view (`4`), then press `v` and `Enter`.

Build from source instead:

```bash
git clone https://github.com/Zen1th53/marshal.git && cd marshal
go build -o marshal ./cmd/marshal && mkdir -p ~/.local/bin && install -m 755 marshal ~/.local/bin/marshal
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
