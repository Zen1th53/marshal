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
[![Platform](https://img.shields.io/badge/platform-Linux%20%7C%20macOS-informational)](#requirements)

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

[One-minute start](#one-minute-start) ·
[How it works](#how-it-works) ·
[Why Marshal](#why-marshal) ·
[The Board](#the-board) ·
[FAQ](#faq) ·
[Docs](#documentation)

</div>

<br>

<p align="center">
  <img src=".github/assets/demo.gif" alt="From an empty folder: marshal init, verify Claude Code, Codex, OpenCode and Antigravity, state a goal, accept the architecture with y, and four agents build four modules in parallel while a different model reviews each one." width="100%">
  <br>
  <sub>Real recording, sped up: empty folder → <code>marshal init</code> → verify four providers → goal → accept the architecture with <code>y</code> → Codex, Claude, OpenCode and Antigravity build four modules in parallel → each hand-in reviewed by a different model.</sub>
</p>

---

## One-minute start

```bash
# 1. Install (Linux & macOS, amd64/arm64, checksum-verified)
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh

# 2. Any Git repository
mkdir my-app && cd my-app && git init -q
echo "# my-app" > README.md && git add . && git commit -qm init

# 3. Prepare it and open the Board
marshal init
marshal
```

Then, in the chat on the left, say what you want built. The Marshal asks a few
questions and proposes an architecture. When the Board shows **NEEDS YOU**,
press `y` to accept it. Workers start, and you watch the run on the Board until
it closes to `main`.

You need at least one provider CLI installed and signed in
([requirements](#requirements)). Marshal uses your existing accounts and plans;
it has no model costs of its own.

---

## How it works

```text
  you ──goal──▶ MARSHAL ──architecture──▶ you accept (y)
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
  Self-approval is not possible.
- **Isolation by default.** A worktree and branch per task, a declared file
  scope, and a hand-in with commit digests and check results.
- **Live and honest state.** The Board shows tasks, lanes, decisions and
  activity as they happen. Nothing is marked done without its check.
- **Shared memory.** What one agent learns about your project is recorded and
  reviewed, and every later agent can use it (local MCP server included).
- **Your tools, your accounts.** Marshal drives the official CLIs you already
  have. It is a single Go binary with state in `.marshal/` inside your repo.

---

## The Board

`marshal` opens a tmux workspace: the Marshal chat on the left, the Board on
the right.

| Key | Action |
|---|---|
| `Alt+←` / `Alt+→` (or `F11`) | Move between the chat and the Board |
| `s` | Say something to the Marshal from the Board |
| `y` | Accept what a **NEEDS YOU** gate asks (architecture, plan) |
| `Enter` | Inspect the highlighted task; on a lane, open the worker's session |
| `v` | Verify a provider |
| `1`–`8` | Views: Board, Host, Memory, Providers, Verify, Security, Plan, Architecture |
| `Ctrl+P` | Command palette |
| `Ctrl+X` | Stop all workers (never the Marshal) |
| `?` | Every key |

The task inspector offers `[a]` approve, `[d]` deny, `[r]` rework, `[x]` cancel
and `[o]` open the worker pane. [All keys →](docs/board-and-keys.md)

---

## Requirements

- Linux or macOS (amd64 or Apple Silicon/arm64)
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
> project directory and review the architecture and plan before you accept
> them. Marshal refuses `/` and your home directory as a project root.

---

## FAQ

**How is this different from running Claude Code or Codex directly?**
You still use them, through their own CLIs. Marshal adds what a single agent
session lacks: a plan you approve, isolation per task, review by a different
model, an independent check before merge, and one Board to watch it all.

**Does Marshal send my code anywhere?**
No. Your code goes only to the providers you choose, through their CLIs.
Marshal's own anonymous telemetry sends a random installation ID, version,
OS/architecture, mode and timestamps, never code, prompts, paths or commands.
Turn it off with `marshal telemetry disable` or `MARSHAL_CLOUD_ENDPOINT=off`.

<details>
<summary><b>Exactly what telemetry sends, on the wire</b></summary>

Telemetry is anonymous and opt-out. It goes to `https://marshal.blackhat.uz`
over HTTPS as JSON. There are exactly two requests, and every field either
party sees is listed below — there is no other payload.

**1. Registration** — once per install, `POST /v1/installations/register`:

```json
{
  "installation_id": "inst-xxxxxxxxxxxxxxxx",
  "public_key": "b3zH1Qc9ukE2y0rW...",
  "client_version": "0.2.4",
  "os": "linux",
  "arch": "amd64"
}
```

- `installation_id` — a random local ID (`inst-` + 16–64 hex). Not tied to you,
  your machine, or your projects; delete `.marshal`/the state dir and it is gone.
- `public_key` — the base64url ed25519 **public** key generated on your machine.
  The private key never leaves it. Used only to authenticate this install's
  own later requests.
- `client_version`, `os`, `arch` — describe the build, e.g. `0.2.4`,
  `linux`, `amd64`. They say nothing about who runs it.

**2. Events** — on start, then a heartbeat every 2 minutes while running,
`POST /v1/telemetry/events` (a batched JSON array):

```json
[
  {
    "kind": "app_start",
    "installation_id": "inst-xxxxxxxxxxxxxxxx",
    "client_version": "0.2.4",
    "os": "linux",
    "arch": "amd64",
    "mode": "standard",
    "occurred_at": "2026-11-09T06:23:46Z"
  }
]
```

| Field | Always sent | What it is |
|---|---|---|
| `kind` | yes | One of: `registration`, `app_start`, `heartbeat`, `mode_change`, `activation_success`, `activation_failure`, `renewal_success`, `renewal_failure`, `error_class` |
| `installation_id` | yes | The same random ID as above |
| `client_version` | yes | Build version, e.g. `0.2.4` |
| `os`, `arch` | yes | e.g. `linux`, `amd64` |
| `mode` | when known | `standard` or `ultra` |
| `occurred_at` | yes | RFC 3339 UTC timestamp of the event |
| `session_id` | optional | A random per-run ID (`sess-…`), when a run is active |
| `error_class` | only on `error_class` | A **coarse category only** — one of `network`, `auth`, `lease_expired`, `lease_invalid`, `rate_limited`, `server`, `client`, `sandbox`, `unknown`. Never a message, stack trace, or detail |

The only request header beyond `Content-Type` is `MARSHAL-Client-Version`.
No cookies, no account, no auth token for telemetry. A failed or timed-out
send is silently dropped; it never blocks or changes what Marshal does.

**Never sent, in any field:** source code or file contents, prompts,
instructions or goals, model output or conversation text, file names, paths or
repository URLs, commit messages, diffs or author identities, environment
variables, API keys or credentials, and terminal commands or their output.

Inspect or turn it off: `marshal telemetry status` · `marshal telemetry disable`
· `MARSHAL_CLOUD_ENDPOINT=off`. [Full policy →](docs/telemetry.md)

</details>

**What does it cost?**
Marshal is free. Agents run on your existing provider accounts and plans.

**Which platforms?**
Linux and macOS. Prebuilt, checksum-verified binaries for amd64 and arm64
(Apple Silicon included); the one-line install picks the right one.

**Is it production-ready?**
It is young (v0.2). The run loop above is tested end to end on fresh
projects with real providers. Expect rough edges, read what you accept, and
[open an issue](https://github.com/Zen1th53/marshal/issues) when something
breaks.

---

## Documentation

- [Installation](docs/installation.md) · [Getting started](docs/getting-started.md) · [How a run works](docs/how-it-works.md)
- [The Board and keys](docs/board-and-keys.md) · [Providers](docs/providers.md) · [Memory and MCP](docs/memory-and-mcp.md)
- [Configuration](docs/configuration.md) · [Telemetry](docs/telemetry.md) · [Troubleshooting](docs/troubleshooting.md)
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
