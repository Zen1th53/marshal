# Set up Marshal with your coding agent

Paste the prompt below into Claude Code or Codex. It installs Marshal, checks Git, tmux and your
coding agents, fixes what is safe to fix, and ends with a short report of what is left. It does not create a project and does not touch your repositories.

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

2. Install. Run exactly:
   curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
   It downloads the release for this machine, verifies its checksum and installs `marshal` into ~/.local/bin (or $MARSHAL_INSTALL_DIR if I set it). It never uses sudo. Ignore its "Next:" lines; this job replaces them. If it fails, show me its last lines and stop.

3. PATH. Run `command -v marshal`. If it prints nothing:
   - for this session: export PATH="$HOME/.local/bin:$PATH"
   - to keep it: add the line export PATH="$HOME/.local/bin:$PATH" to my shell's startup file (~/.zshrc for zsh, the macOS default; ~/.bashrc for bash), only if that file does not already contain it. Tell me which file you changed.
   Then `marshal version` must print a line that starts with "Marshal v".

4. Readiness. Run `marshal setup status`. It only reports and changes nothing. In this empty folder "No project is open" and "This directory is not part of a Git repository" are expected; ignore them. Tell me about any other line whose status is not READY or OPTIONAL.

5. Git. `git --version` must work. If Git is missing:
   - macOS: ask me to run `xcode-select --install` (it opens a dialog), or run `brew install git` if Homebrew is installed.
   - Linux: show me the command for this system (apt: sudo apt install git · dnf: sudo dnf install git · pacman: sudo pacman -S git · zypper: sudo zypper install git) and run it only after I say yes.

6. tmux 3.3a or newer. Run `tmux -V`. If tmux is missing or older than 3.3a:
   - macOS with Homebrew: run `brew install tmux` (or `brew upgrade tmux`); it needs no sudo.
   - Linux: show me the command (same package managers as step 5, package tmux) and run it only after I say yes. Check `tmux -V` again afterwards.
   - If the system's tmux is still older than 3.3a (Ubuntu 22.04 ships 3.2a), do not build it from source. Tell me I need Ubuntu 24.04 or newer, Debian 12 or newer, or a tmux built from source.

7. Coding agents. For each of claude, codex, opencode and agy: run `command -v <name>` and, if found, `<name> --version`. Marshal needs at least one; with two or more, a different model reviews each result. Do not install any of them unless I ask.
   Sign-in, read-only only:
   - codex: run `codex login status`.
   - claude, opencode, agy: there is no read-only check. Report "installed, sign-in not checked" and tell me how to sign in: Claude Code: run `claude` and use /login. OpenCode: run `opencode auth login`. Antigravity: sign in inside the Antigravity app.

8. Telemetry. Run `marshal telemetry status`. Tell me in one line: Marshal sends anonymous usage telemetry (a random install ID, the version, OS and CPU type, and timestamps; never code, prompts or file paths), and `marshal telemetry disable` turns it off. Do not change it yourself.

9. Report. Finish with this short report and nothing after it:
   Marshal: <version> at <path> (PATH: ok | added to <file> | needs fixing)
   Platform: <Linux | macOS | WSL2> <cpu>
   Git: <version | missing>
   tmux: <version> (<ok | too old | missing>)
   Agents: <name: version, signed in | not signed in | sign-in not checked>, one per installed agent; "none found" if none
   Fixed: <what you changed, or "nothing">
   Needs you: <one line per remaining step, or "nothing">
   Telemetry: <on | off> (marshal telemetry disable turns it off)
   Next: in one of your Git repositories run `marshal init`, then `marshal`.
````
<!-- setup-prompt:end -->

What the prompt does not do: it never runs `marshal init` or opens the Board, never signs in for
you, and never uses `sudo` without asking. To check a project later, run `marshal doctor` inside
it.
