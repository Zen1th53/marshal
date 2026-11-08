# Installation

Marshal is published as a standalone binary for Linux on x86_64 (`amd64`) and ARM64 (`arm64`).

> [!WARNING]
> **Host Execution Notice**: Worker processes execute directly on your host machine under your user account without a sandbox. Worktrees isolate Git changes, not process execution or network activity. Always review goals, plans, and generated code before approving work.

---

## Prerequisites

Before installing Marshal, make sure your system has:

- **Linux OS**: amd64 or arm64 architecture.
- **Git**: Required for project initialization, branch management, and worktree isolation.
- **tmux 3.3a or newer**: Required by `marshal` and `marshal tui` for window management, docked chat, and interactive popups.
- **Provider CLIs**: Install and log into the AI agent tools you plan to use:
  - Claude Code (`claude`)
  - OpenAI Codex (`codex`)
  - OpenCode (`opencode`)
  - Google Antigravity (`agy`)

Marshal does not bundle provider binaries or manage API subscriptions; it uses the authenticated CLIs found on your `$PATH`.

---

## Recommended: Install via `install.sh`

The official installer downloads the release binary, verifies its SHA-256 checksum against published release hashes, and installs it to your local binary folder. Nothing runs with `sudo` and no system directories are modified.

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

### Customizing the Installation

You can override default settings using environment variables:

| Variable | Description | Default |
|---|---|---|
| `MARSHAL_INSTALL_DIR` | Destination folder for the `marshal` binary | `$HOME/.local/bin` |
| `MARSHAL_VERSION` | Specific release tag to install (e.g. `v0.2.0`) | Latest release |

Example:

```bash
MARSHAL_INSTALL_DIR="/usr/local/bin" MARSHAL_VERSION="v0.2.0" \
  curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

Ensure your installation directory is included in your shell's `$PATH`:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

---

## Building from Source

To compile Marshal directly from the Git repository, you need a Go compiler (Go 1.25 or newer):

```bash
# 1. Clone the repository
git clone https://github.com/Zen1th53/marshal.git
cd marshal

# 2. Build the binary
go build -o marshal ./cmd/marshal

# 3. Install to your local path
install -Dm755 marshal "$HOME/.local/bin/marshal"
```

---

## Verifying the Installation

Check the installed version and verify runtime dependencies:

```bash
# Display binary version, commit hash, and build timestamp
marshal version

# Run diagnostics on Git, tmux, and available tools
marshal doctor

# Probe installed agent provider CLIs
marshal doctor --probe-providers
```

`marshal doctor` checks system prerequisites, detects installed agent CLIs, probes provider availability, and reports any configuration issues.

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
