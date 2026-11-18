# Troubleshooting

This guide addresses common questions, error messages, and operational situations encountered while using Marshal.

---

## 1. Uninitialized Project Message

### The Error

Opening `marshal` in a Git project sets Marshal up there by itself, as `marshal init` would, and says which files it committed. Other commands, such as `marshal status`, may instead print:

```text
Marshal isn't set up in this project yet. Run `marshal` here and it sets the project up.
```

or, when some of Marshal's files exist but are broken:

```text
Some of Marshal's files in this project are damaged. Run `marshal doctor` to see which.
```

### Why it Happens

Marshal requires an initialized, valid Git project before launching the runtime or opening the Board. This ensures your repository has a valid Git root, tracking baseline, and state storage.

### How to Fix It

1. **Ensure you are in the correct directory**: Check that your current working directory is the intended project root.
2. **Ensure Git is initialized**:
   ```bash
   git status
   ```
   If Git is not initialized or the repository has no commits yet:
   ```bash
   git init
   git commit --allow-empty -m "Initial commit"
   ```
3. **Open Marshal**. In a Git repository, first open sets up the project automatically and reports any files committed:
   ```bash
   marshal
   ```
   This creates missing project metadata (`CAPABILITIES.yaml`, `PACK-VERSION.yaml`, `RUNTIME-VERSION.yaml`) and commits those files as `Initialize MARSHAL project`. You can also run `marshal init` yourself as an optional explicit setup command.
4. **Verify project readiness**:
   ```bash
   marshal doctor
   ```

---

## 2. Provider Limits, Rate Limits, and Quotas

### Symptoms

- A worker lane hangs or marks a task as `failed` / `returned`.
- The provider reports HTTP 429, token quota exhaustion, or authentication expiration.
- The provider lane shows as `limited` in the Providers view (`4`).

### How to Fix It

1. **Verify native CLI authentication**:
   Make sure you are logged into the relevant provider CLI on your host machine:
   - For Claude Code: run `claude` or `claude login`
   - For Codex: run `codex`
   - For OpenCode: verify API keys in your environment (`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, etc.)
   - For Antigravity: run `agy login` or `agy`
2. **Probe providers**:
   ```bash
   marshal doctor --probe-providers
   ```
3. **Select a different model**:
   If a specific model is rate-limited, switch to another model using the Board command line (`/`):
   ```text
   /model select claude claude-3-7-sonnet
   /model select codex o3-mini
   /model select agy fast
   ```
4. **Reduce concurrency**:
   If you hit aggressive API rate limits, reduce the number of concurrent worker lanes:
   ```text
   /marshal settings ultra-concurrency 1
   ```

---

## 3. Resuming a Stopped Run

### What Happens When a Run Stops

If Marshal is interrupted (e.g. terminal closed, machine rebooted, or workers stopped with `Ctrl+X`), work is not lost:
- Tasks that already merged remain in their respective Git commits.
- Task outputs, events, and candidate memories are preserved in `.marshal/state.db`.
- Unfinished task branches remain in their temporary worktrees.

### How to Resume

To resume where you left off:

1. Return to the project root and start Marshal:
   ```bash
   cd /path/to/project
   marshal
   ```
2. Marshal automatically detects the latest unfinished run in `.marshal/state.db` and reloads the Board.
3. If the run was waiting for you, the **`NEEDS YOU`** prompt reappears. Reply as requested; for the architecture, press **`y`** to open it in your browser and accept it there.
4. If workers were stopped while dispatching, Marshal releases stale leases, reschedules incomplete tasks, and resumes execution.

---

## 4. Diagnostics and Health Checks

Use Marshal's built-in diagnostic tools to troubleshoot issues:

```bash
# Basic system checks (Git version, state database, directories)
marshal doctor

# Include live execution probes for all installed provider CLIs
marshal doctor --probe-providers

# Deep diagnostics (verifies state integrity and repository layout)
marshal doctor --deep

# Check raw task logs from outside the TUI
marshal logs <TASK-ID>

# Check current project status and active tasks
marshal status
```

## Governed Workers on macOS or Linux arm64

### Symptom

The run stops with **"This computer cannot run governed workers (…). Governed workers need Linux on amd64."**

### Why

Governed workers run inside Linux confinement (bubblewrap and a socket supervisor built for amd64). macOS and Linux arm64 have neither, so every worker would fail the same way. Earlier versions reported this as a network problem, retried and cooled the providers down; it is now named at once.

### How to Fix It

Open the project with Marshal on a Linux amd64 machine (a server, a cloud VM, or an x86_64 VM). Planning, requirements and the architecture review work on any platform.

## Marshal Was Updated but Still Behaves the Old Way

The control centre keeps running in its own tmux session after you close the terminal, so an update is not loaded until it restarts. When you start `marshal` after an update it now asks **"Restart it now to load the update? [Y/n]"**. Without a terminal it prints how to do it: type `/quit` in the control centre, then run `marshal` again.

## The Architecture Was Refused When the Marshal Sent It

`marshal_project_contract_submit` now refuses an architecture the browser could not draw, with the rule. The most common cause is a package path used as a component ID: IDs are short identifiers (letters, digits, `.`, `_`, `-`; no `/`); put the package path in the component's `paths` instead.
