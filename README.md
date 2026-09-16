# MARSHAL

[![CI](https://github.com/Zen1th53/marshal/actions/workflows/ci.yml/badge.svg)](https://github.com/Zen1th53/marshal/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/Zen1th53/marshal?color=blue)](https://github.com/Zen1th53/marshal/releases)
[![Go Version](https://img.shields.io/github/go-mod/go-version/Zen1th53/marshal)](https://go.dev)
[![License: AGPL v3](https://img.shields.io/badge/License-AGPL--3.0--only-blue.svg)](LICENSE)

**One workspace for every coding agent you use.**

Claude Code and Codex, in the same project, sharing one memory — with sandboxed
execution and a record of everything they did.

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

---

## The problem

You run Claude Code in one terminal and Codex in another. Each keeps its own history,
so neither knows what the other changed. When a session ends its reasoning is gone —
scattered across scrollback you will never read again. And both of them are editing
your repository with your full privileges, with nothing between them and the disk.

MARSHAL is the layer in between: a local control plane that runs agents in sandboxed
cells, records what they actually do, and lets them build on each other's work.

Everything stays on your machine. Nothing is uploaded, and nothing runs unless you
asked for it.

---

## What you get

### One workspace, every agent

```bash
marshal tui
```

```
/claude          open a native Claude Code session
/codex           open a native Codex session
F8 / F7          the same two, one keypress
/claude continue resume where Claude left off
/status          project, run and agent state
```

Sessions are **native**. Claude Code runs as Claude Code: your configuration, your
authentication, your skills, MCP servers and plugins, its own permission prompts.
MARSHAL does not proxy the provider, rewrite its prompts, or stand between the agent
and you. Adapters also ship for OpenCode, Gemini CLI and Antigravity.

Switching is one keypress once you leave the current agent. Open two terminals and
you can run Claude and Codex **at the same time** in the same repository — they share
one project memory and keep their own import state, so neither disturbs the other.

### Memory that saves itself

You do not press save. From the moment an agent starts, MARSHAL records to the
project database every two seconds and again on exit:

- **the conversation** — what you asked, what the agent answered
- **every tool call** — the commands it ran, the files it opened
- **the code it wrote or deleted**, as a diff, kept whole rather than summarized

Then search it, across agents and across time:

```bash
/memory search watcher      # in the workspace
marshal memory recall ...   # or from the shell
```

What is **never** stored: the model's hidden reasoning, and any credential — secrets
are redacted before anything is written. Tool output is bounded, so one huge dump
cannot crowd out the record, and a truncated payload always says so rather than
passing itself off as complete.

Records are kept as **observations, not verified facts**. MARSHAL tells you where
each came from instead of presenting an agent's guess as settled truth.

### Agents that build on each other

This is the part you cannot get by running the CLIs yourself.

When an agent starts, it receives a briefing compiled from what the **other** agents
have already done in this project — recent sessions, what they ran, what they
changed. Codex opens knowing what Claude just did, and the reverse.

That briefing is a snapshot, so MARSHAL keeps it current: while a session runs it
watches the other agents and appends their work to a live inbox the running agent can
read (`.marshal/inbox/<agent>.md`). Two agents working in parallel can follow each
other without you relaying anything by hand.

Both the briefing and the inbox say plainly what they are: **untrusted data, not
instructions**. They quote another agent's output, which may contain anything it
happened to read, and nothing in them overrides you.

### Security you do not have to configure

- **Sandboxed execution.** Agent processes run in isolated cells with a read-only
  root, private runtime directories and no network by default.
- **Isolated worktrees.** Work happens on a dedicated Git worktree and branch. Your
  working tree is never the experiment.
- **Fail closed.** When a boundary cannot be enforced, the run stops. It does not
  continue with the policy unenforced and tell you afterwards.
- **Secrets never land.** Credentials are redacted from stored output and never enter
  durable memory.
- **Nothing starts by accident.** Plain text in the workspace runs nothing. A command
  typed without its slash is answered with the command it looks like. Launching an
  agent is always something you asked for by name.
- **Delegation is earned, not toggled.** Letting MARSHAL act without asking each time
  requires a cryptographically verified entitlement. A local flag cannot grant it, and
  the test suite includes bypass attempts that must fail.

### Evidence you can check later

Command output and artifacts are content-addressed and linked to the commit they
produced, so "what did this run actually change?" has an answer you can verify
instead of a log you have to trust.

---

## Install

**Linux, one command:**

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

Resolves the latest release, verifies the download against its published checksums,
and installs to `~/.local/bin`. No `sudo`; nothing outside the install directory is
touched. A download that fails verification is not installed.

```bash
# Choose the location, or pin a version
MARSHAL_INSTALL_DIR=/usr/local/bin MARSHAL_VERSION=v0.0.1 \
  sh -c "$(curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh)"
```

**From a release archive** — the archive and `checksums.txt` are on the
[latest release](https://github.com/Zen1th53/marshal/releases/latest):

```bash
sha256sum -c checksums.txt --ignore-missing
tar -xzf marshal_<version>_linux_amd64.tar.gz
install -Dm755 marshal "$HOME/.local/bin/marshal"
```

**From source** — requires Go and `git`:

```bash
go install github.com/Zen1th53/marshal/cmd/marshal@latest
```

---

## Quick start

```bash
cd /path/to/your/repository

marshal init        # create the project runtime
marshal doctor      # check the host, and probe the agent CLIs you have installed
marshal tui         # open the workspace
```

Then, in the workspace:

```
/claude                     work with Claude Code
                            ...exit the agent when you are done
/codex                      hand over to Codex — it already knows what changed
/memory search <anything>   ask the project what happened
```

---

## Modes

```
/mode manual     the default working mode
/mode auto       switch the session's working mode
/mode ultra      refused without a verified entitlement
/ultra           why this session is Standard, and how to ask for more
```

`manual` and `auto` are **session preferences**: they are recorded and shown in the
status line. They do not, on their own, hand anything the right to act without you.

Autonomous delegation — MARSHAL deciding instead of asking each time — is gated on a
cryptographically verified entitlement, and that gate is consulted on every attempt
rather than cached at startup. A local setting cannot grant it; `/mode ultra` without
a lease is simply refused. Without an entitlement the session runs as Standard and
keeps asking, which is the intended behaviour rather than a degraded one.

---

## Requirements

| | |
|---|---|
| **Linux** | The supported platform for sandboxed execution. No equivalent backend exists for macOS or Windows. |
| **Bubblewrap** (`bwrap`) | Required for execution cells. |
| **Git** | MARSHAL operates on a Git repository. |
| **Agent CLIs** | Install and authenticate the ones you want. MARSHAL bundles none of them and proxies none of them. |

Run `marshal doctor` to see what is present and what is missing.

---

## Limitations

Stated plainly, because a control plane that overstates its guarantees is worse than
none:

- **Network egress is not granularly filtered.** Runs that require network access fail
  closed rather than proceeding with unenforced policy.
- **Linux only** for sandboxed execution.
- **One agent per terminal.** A native session owns its terminal; running two at once
  means two terminals.
- **Vector search needs an embedding provider.** Exact and lexical search work without
  one.
- **No third-party security audit.** Automated suites cover the security invariants;
  that is not the same as external certification.

---

## Documentation

| | |
|---|---|
| [Getting started](docs/getting-started.md) | Step-by-step tutorial |
| [Workspace guide](docs/tui.md) | Native sessions, memory capture and cross-agent exchange |
| [CLI reference](docs/cli.md) | Every command |
| [Security model](docs/security-model.md) | Threat model and isolation boundaries |
| [Providers](docs/providers.md) | Supported agent CLIs and configuration |
| [Troubleshooting](docs/troubleshooting.md) | Diagnostics and recovery |
| [Documentation hub](docs/README.md) | Everything else |

---

## Community and Enterprise

MARSHAL Community is a local, single-node, project-scoped runtime — the whole of this
repository. Enterprise adds a remote web control plane, multi-node fleet
orchestration, centralized approvals and multi-user access control under a separate
commercial license. No web routes or listeners ship in the Community binary.

---

## Contributing

Issues and pull requests are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md)
and [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) first.

Security vulnerabilities: follow [SECURITY.md](SECURITY.md) and report privately
rather than opening a public issue.

---

## License

- **Community** — GNU Affero General Public License v3.0 only. See [LICENSE](LICENSE).
- **Commercial** — licenses without AGPL copyleft are available. See
  [LICENSING.md](LICENSING.md).
- **Historical releases** up to `runtime-v0.4.0` remain under their original
  Apache-2.0 grants. See [docs/legal/LICENSE-HISTORY.md](docs/legal/LICENSE-HISTORY.md).
- **Third-party** attributions: [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
