# Install MARSHAL

This page takes you from nothing to a working `marshal` command.

## What you need

- [x] A computer running **Linux** (64-bit Intel/AMD or ARM).
- [x] A **project folder that uses Git**. If you are not sure, open a terminal
      in the folder and run `git status`. If it prints an error, run
      `git init` first.
- [x] At least one **AI coding agent** installed and signed in:
      [Codex](https://github.com/openai/codex),
      [Claude Code](https://docs.anthropic.com/en/docs/claude-code),
      OpenCode or Antigravity's `agy` command.
      See [Connect your AI agents](agents.md).

## Step 1: Install

Open a terminal and run:

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

The installer downloads MARSHAL, checks the download against the published
checksums, and puts
the `marshal` program in `~/.local/bin`. It does not use `sudo` and does not
change anything else on your computer.

??? tip "Installing a test version (release candidate)"
    The installer picks the latest stable release. To install a specific
    version, such as a release candidate, name it:

    ```bash
    curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | MARSHAL_VERSION=v0.0.5-rc.5 sh
    ```

## Step 2: Check that it works

```bash
marshal version
```

You should see the version number. If the terminal says
`marshal: command not found`, your system does not look in `~/.local/bin` yet.
Add this line to the end of `~/.bashrc` (or `~/.zshrc` if you use zsh), then
open a new terminal:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

## Step 3: Install Bubblewrap

MARSHAL uses a small tool called Bubblewrap to keep agents inside their own
working copy. Install it with your system's package manager:

=== "Ubuntu / Debian"

    ```bash
    sudo apt-get install bubblewrap
    ```

=== "Fedora"

    ```bash
    sudo dnf install bubblewrap
    ```

=== "Arch"

    ```bash
    sudo pacman -S bubblewrap
    ```

Without it, MARSHAL can refuse to run planned work rather than run it
unprotected.

## Step 4: Set up your project

Go to your project folder and run:

```bash
cd path/to/your-project
marshal init
marshal doctor
```

`marshal init` adds MARSHAL's settings to the project. It does not overwrite
anything that is already there. `marshal doctor` checks that everything is in
place and tells you what to fix if something is missing.

That is it. Continue with [Your first project](first-project.md).

??? note "Building from source instead"
    If you prefer to build MARSHAL yourself, you need Go 1.25.13 or newer:

    ```bash
    git clone https://github.com/Zen1th53/marshal.git
    cd marshal
    go build -o ./bin/marshal ./cmd/marshal
    ./bin/marshal version
    ```

    Then use `./bin/marshal` instead of `marshal`, or copy it to
    `~/.local/bin`.
