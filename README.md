# MARSHAL

[![CI](https://github.com/Zen1th53/marshal/actions/workflows/ci.yml/badge.svg)](https://github.com/Zen1th53/marshal/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/Zen1th53/marshal?color=blue)](https://github.com/Zen1th53/marshal/releases)
[![Go Version](https://img.shields.io/github/go-mod/go-version/Zen1th53/marshal)](https://go.dev)
[![License: AGPL v3](https://img.shields.io/badge/License-AGPL--3.0--only-blue.svg)](LICENSE)

**A local control plane for coding agents.**

Run Claude Code, Codex and other agent CLIs inside one governed workspace: sandboxed
execution, durable shared memory, and a record of what each agent actually did.

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

---

## Why

Agent CLIs edit files and run commands with your full privileges. Each one keeps its
own history, so they cannot see each other's work, and when a session ends its
reasoning is scattered across terminal scrollback.

MARSHAL sits between you and those CLIs. It runs them in isolated execution cells,
records what they do in a project-local database, and lets them build on each other's
work instead of starting blind.

Nothing runs unless you ask for it. When a security boundary cannot be enforced,
MARSHAL fails closed rather than proceeding.

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

**From a release archive** — download the archive and `checksums.txt` from the
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

Release binaries are built for Linux on **amd64** and **arm64**. Sandboxed execution
requires [Bubblewrap](https://github.com/containers/bubblewrap) (`bwrap`). Provider
CLIs and their credentials are yours: MARSHAL does not bundle or proxy them.

---

## Quick start

```bash
cd /path/to/your/repository
marshal init        # create the project runtime
marshal doctor      # check the host, and optionally probe installed agent CLIs
marshal tui         # open the workspace
```

---

## The workspace

`marshal tui` opens a terminal workspace where you drive agents by name.

```
/claude          open a native Claude Code session
/codex           open a native Codex session
/status          what the project and the current run look like
/memory search   search what agents have done here
```

**Agents run natively.** A session uses your own configuration, authentication,
skills, MCP servers and plugins, and its own permission prompts. MARSHAL does not
stand between the agent and you.

**Everything is recorded.** Conversation, tool calls, and the code an agent wrote or
deleted are captured to the project database as it works. Hidden reasoning is never
stored, and tool output is bounded so one large dump cannot crowd out the record.

**Agents build on each other.** A starting session receives a briefing compiled from
what the *other* agents have done in this project, and MARSHAL keeps it current while
the session runs — so Codex can pick up where Claude left off, and the reverse.

**Nothing starts by accident.** Plain text runs nothing, and a command typed without
its slash is answered with the command it looks like. Launching an agent is always
something you asked for.

---

## What it does

| | |
|---|---|
| **Sandboxed execution** | Agent processes run in isolated cells with a read-only root, private runtime directories and no network by default. |
| **Isolated worktrees** | Work happens on a dedicated Git worktree and branch, so your working tree is never the experiment. |
| **Shared memory** | A project-local record of sessions, decisions and outcomes, searchable across agents and across time. |
| **Authorization** | Capability grants are explicit and time-bounded; risk is assessed before a command runs, not after. |
| **Secret handling** | Credentials are redacted from stored output and never enter durable memory. |
| **Evidence** | Command output and artifacts are content-addressed and linked to the commit they produced. |
| **Interoperability** | Model Context Protocol and Agent-to-Agent endpoints for tools that speak them. |

For the command surface see the [CLI reference](docs/cli.md); for how the workspace
behaves see the [TUI guide](docs/tui.md).

---

## Requirements

- **Linux** — the supported platform for sandboxed execution. There is no equivalent
  sandbox backend for macOS or Windows.
- **Bubblewrap** (`bwrap`) — required for execution cells.
- **Git** — MARSHAL operates on a Git repository.
- **Agent CLIs** — install and authenticate the ones you want to use.

Run `marshal doctor` to see what is present and what is missing.

---

## Limitations

Stated plainly, because a control plane that overstates its guarantees is worse than
none:

- **Network egress is not granularly filtered.** Runs that require network access fail
  closed rather than proceeding with unenforced policy.
- **Linux only** for sandboxed execution.
- **Vector search needs an embedding provider.** Exact and lexical search work without
  one.
- **No third-party security audit.** Automated suites cover the security invariants;
  that is not the same as external certification.

---

## Documentation

| | |
|---|---|
| [Getting started](docs/getting-started.md) | Step-by-step tutorial |
| [TUI guide](docs/tui.md) | The workspace, native sessions and shared memory |
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
