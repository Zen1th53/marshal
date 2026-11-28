# Installation

Marshal is published as a standalone binary for Linux and macOS on x86_64 (`amd64`) and ARM64 (`arm64`). On Windows, use WSL2. In the standard release, workers run on your host in Git worktrees on Linux and macOS; Antigravity (`agy`) workers run on Linux only. Sandboxed, socket-confined workers require `-tags governed` and Linux amd64.

Prefer to let your coding agent do it? Paste the [setup prompt](SETUP-PROMPT.md) into Claude Code or Codex.

> [!WARNING]
> **Host Execution Notice**: Worker processes execute directly on your host machine under your user account without a sandbox. Worktrees isolate Git changes, not process execution or network activity. The Marshal plans and approves the work itself; accepting the architecture is your one gate, so review it before you accept, and stop the run at any time with `Ctrl+X`.

---

## Prerequisites

Before installing Marshal, make sure your system has:

- **Linux or macOS**: amd64 or arm64 (Apple Silicon) architecture. Workers in `-tags governed` builds need Linux amd64.
- **Git**: Required for project initialization, branch management, and worktree isolation.
- **tmux 3.3a or newer**: Required by `marshal` and `marshal tui` for window management and the docked chat.
- **Provider CLIs**: Install and log into the AI agent tools you plan to use:
  - Claude Code (`claude`)
  - OpenAI Codex (`codex`)
  - OpenCode (`opencode`)
  - Google Antigravity (`agy`)

Marshal does not bundle provider binaries or manage API subscriptions; it uses the authenticated CLIs found on your `$PATH`.

---

## Recommended: Install via `install.sh`

The official installer downloads the release binary, verifies its SHA-256 checksum against published release hashes, installs it to your local binary folder, and then sets the machine up so `marshal` works right away:

- **PATH** — if the install folder is not on your `PATH`, it adds one line to your shell's startup file (`~/.zshrc`, `~/.bashrc` or `~/.bash_profile` on macOS, fish's `conf.d`, or `~/.profile`) and tells you which file it changed.
- **Git and tmux 3.3a+** — checked. On macOS with Homebrew a missing one is installed with `brew install` (no password). On Linux the installer asks before running the `sudo` command for your package manager; with no terminal to answer (CI, a coding agent) it prints the command instead and never runs `sudo`.
- **Coding agents** — lists which of Claude Code, Codex, OpenCode and Antigravity it found. It never installs or signs in to them.
- **Anonymous telemetry** — registers this installation's random ID once, so the install is counted (see [Telemetry](telemetry.md); `marshal telemetry disable` turns it off).

It ends with what is left to do, if anything, and the command to start.

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

To install without being counted, run the installer with telemetry off: `curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | MARSHAL_CLOUD_ENDPOINT=off sh`. `marshal telemetry disable` keeps it off afterwards.

### Customizing the Installation

You can override default settings using environment variables:

| Variable | Description | Default |
|---|---|---|
| `MARSHAL_INSTALL_DIR` | Destination folder for the `marshal` binary | `$HOME/.local/bin` |
| `MARSHAL_VERSION` | Specific release tag to install (e.g. `v0.4.0`) | Latest release |
| `MARSHAL_NO_MODIFY_PATH` | `1` leaves shell startup files alone | unset |
| `MARSHAL_INSTALL_DEPS` | `0` only reports a missing Git or tmux, never installs it | `1` |
| `MARSHAL_INSTALL_YES` | `1` answers yes to the installer's questions | unset |

Example:

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | \
  MARSHAL_INSTALL_DIR="/usr/local/bin" MARSHAL_VERSION="v0.4.0" sh
```

The installer puts the installation directory on your `PATH` for new terminals; for the terminal you installed from it prints the one line to run:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

---

## Building from Source

To compile Marshal directly from the Git repository, you need a Go compiler (Go 1.26.11 or newer):

```bash
# 1. Clone the repository
git clone https://github.com/Zen1th53/marshal.git
cd marshal

# 2. Build the binary
go build -o marshal ./cmd/marshal

# 3. Install to your local path
mkdir -p "$HOME/.local/bin" && install -m 755 marshal "$HOME/.local/bin/marshal"
```

---

## Verifying the Installation

Check the installed version and verify runtime dependencies:

```bash
# Display binary version, commit hash, and build timestamp
marshal version

# Inside a project: check Git, the project's state and its folders
marshal doctor

# Probe installed agent provider CLIs
marshal doctor --probe-providers
```

`marshal doctor` checks system prerequisites, repository layout, and project state; add `--probe-providers` to check installed provider CLIs and versions.

---

## Updating Marshal

To check for or install updates from within the terminal:

```bash
# Check update status
marshal update

# Install latest available update
marshal update install
```

You can also re-run the `install.sh` script at any time to upgrade to the latest release.
