# MARSHAL Troubleshooting Guide

**Runtime Milestone**: `v0.4.0`

This guide provides symptom-based troubleshooting for engineers operating MARSHAL local control plane and provider adapters.

---

## 1. Environment & Diagnostics

### Symptom: `marshal doctor` reports `bwrap` is missing / unavailable
- **Meaning**: Linux `bubblewrap` binary is not found on `$PATH`.
- **Impact**: Execution falls back to process isolation without strong filesystem namespaces.
- **Fix**: Install bubblewrap using your host package manager:
  ```bash
  # Ubuntu / Debian
  sudo apt-get install bubblewrap

  # Fedora / RHEL
  sudo dnf install bubblewrap

  # Arch Linux
  sudo pacman -S bubblewrap
  ```

---

## 2. Daemon & Service Lifecycle

### Symptom: Daemon will not start / `socket state or permissions are unsafe`
- **Meaning**: Socket `.marshal/runtime.sock` already exists or has unsafe file permissions.
- **Inspection**:
  ```bash
  ls -la .marshal/runtime.sock
  ```
- **Fix**: Verify if another `marshal daemon` instance is running (`pgrep -fl marshal`). If no process is running, safely remove the stale socket file:
  ```bash
  rm -f .marshal/runtime.sock
  ```

### Symptom: Stale PID file detected on daemon startup
- **Meaning**: `.marshal/pid` contains a PID from an unclean daemon shutdown.
- **Behavior**: MARSHAL automatically performs process liveness checks (`syscall.Kill(pid, 0) == ESRCH`) on startup. If the old PID is dead, MARSHAL unlinks the stale PID and socket files automatically.
- **Manual Fix**: If needed, remove `.marshal/pid` and restart `marshal daemon`.

---

## 3. Provider Binary & Execution Issues

### Symptom: `codex` binary not found
- **Meaning**: `codex` binary is missing from `$PATH`, `~/.local/bin`, and `/usr/local/bin`.
- **Fix**: Ensure `codex-cli` is installed and add `~/.local/bin` to your `$PATH`:
  ```bash
  export PATH="$HOME/.local/bin:$PATH"
  ```

### Symptom: `opencode` binary not found
- **Meaning**: `opencode` binary is missing from PATH.
- **Fix**: Install OpenCode CLI and verify with `opencode --version`.

### Symptom: Ollama service unreachable (`http://localhost:11434`)
- **Meaning**: Ollama daemon is offline or listening on an unexpected port.
- **Inspection**:
  ```bash
  curl -v http://localhost:11434/api/tags
  ```
- **Fix**: Start the Ollama background service:
  ```bash
  ollama serve
  ```

### Symptom: Local Ollama model missing / unavailable
- **Meaning**: The requested model specified in `--model` has not been pulled into Ollama.
- **Fix**: Pull the model:
  ```bash
  ollama pull qwythos-9b
  ```

### Symptom: Local model returns conversational text instead of executing tool calls
- **Meaning**: The selected model lacks OpenCode tool-calling schema support.
- **Explanation**: Conversational text capability does NOT imply tool-calling capability.
- **Fix**: Switch to a verified tool-calling model such as `qwythos-9b`.

---

## 4. Task Execution, Worktrees & Recovery

### Symptom: Task returns `ErrConflict` or "worker produced no commit"
- **Meaning**: The provider process ran inside `.marshal/worktrees/TASK-ID` but did not create any modified files or git commits.
- **Inspection**: View stdout/stderr logs with `marshal logs TASK-ID`.
- **Safe Recovery**: Check if the task prompt was too vague or if the model failed to issue tool calls. Re-run or cancel the task with `marshal cancel TASK-ID`.

### Symptom: Task is stuck or long-running
- **Meaning**: The LLM provider is processing a complex task or waiting on response generation.
- **Inspection**: Check active logs:
  ```bash
  marshal logs TASK-ID
  ```
- **Cancellation**: Cancel the running task safely:
  ```bash
  marshal cancel TASK-ID
  ```

### Symptom: Git worktree preserved after interrupted task
- **Meaning**: An unhandled termination left `.marshal/worktrees/TASK-ID` intact.
- **Safe Cleanup**: Use Git worktree commands to inspect or prune stale worktrees:
  ```bash
  git worktree list
  git worktree prune
  ```

---

## 5. Protocol & Authentication Issues

### Symptom: MCP or A2A request returns `HTTP 401 Unauthorized`
- **Meaning**: Missing or invalid Bearer token in `Authorization` header.
- **Fix**: Generate a valid token using `marshal auth token create --name <NAME>` and pass `Authorization: Bearer marshal_token_...`.

### Symptom: Gemini returns HTTP 429 Rate Limit / Quota Exhausted
- **Meaning**: Gemini CLI exceeded API quota limits.
- **Behavior**: MARSHAL probes Gemini non-destructively; quota limits mark Gemini as `CAPABILITY-PROBED / UNVERIFIED` without crashing the runtime.

### Symptom: Claude returns OAuth Session Expired
- **Meaning**: Claude Code OAuth session expired.
- **Behavior**: MARSHAL marks Claude as `CAPABILITY-PROBED / UNVERIFIED`. Re-authenticate Claude Code (`claude login`) to restore full functionality.
