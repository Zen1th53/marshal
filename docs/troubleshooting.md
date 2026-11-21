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
   - For Antigravity: run `agy` once and sign in
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
   or lower the limit of the provider that is rate-limited, for example `/marshal settings claude-limit 1` ([provider limits](configuration.md#provider-limits)).

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
3. If the run was waiting for you, its card opens again (`YOUR TURN` in the top bar, and *from you* names it). Reply as requested; for the architecture, press **`a`** (or **`y`**) to open it in your browser and accept it there.
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

# Check raw task logs from outside the TUI (needs `marshal daemon`, see below)
marshal logs <TASK-ID>

# Check current project status and active tasks
marshal status
```

`marshal status` and `marshal tasks` read the project directly. `marshal logs`, `marshal events`, `marshal artifacts`, `marshal agents`, `marshal run`, `marshal verify`, `marshal reconcile`, `marshal cancel` and `marshal task show|claim|release|import` work only through Marshal's background service: start it with `marshal daemon` in another terminal in the project first. If Marshal is already open for the project in another window, use that window instead.

`marshal run` and `marshal task claim` need a registered agent; register one with `marshal agent register --name NAME --role developer` (also through the background service).

`marshal state backup --output PATH` copies the project's state database, `marshal state verify-backup PATH` checks a copy, and `marshal state restore PATH` restores one while Marshal is stopped.

`marshal egm status` reports whether the optional EGM design-review plugin is installed for each provider, and `marshal egm install --provider PROVIDER` prints its install steps; without it Marshal uses its built-in design review.

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

## A Decision Is Stuck as `[Pasted text #N]` in the Marshal Chat

### Symptom

The Board sent the Marshal a decision or request, but the Claude Code chat shows it collapsed as `[Pasted text #1]` in its input box and never acts on it. The request later expires and the run waits.

### Why

Claude Code draws its prompt with a no-break space, so earlier Boards did not recognise their own collapsed paste and never pressed `Enter`. This is fixed in **0.3.5**: the Board submits its paste within a second, and a restarted Board also submits the paste its predecessor left. Text you typed yourself in the chat is never submitted for you.

### How to Fix It

- **0.3.5 or newer**: nothing to do. If *from you* on the NOW line says `send or clear your unsent text in the Marshal chat`, the input holds text you typed: send it or clear it, and the Board delivers its next request.
- **Older versions**: press `F11` to go to the chat and press `Enter` to submit the pasted text. If the request already expired, clear the input and press `s` on the Board to ask the Marshal to read its inbox again. Then update Marshal (`F10`, or the installer) to get the fix.

## The Marshal Chat Restarted and Its Answers Are Refused

### Symptom

After the Marshal chat restarted (for example after it crashed or its session was resumed), the Marshal's answers or tool calls are refused with messages such as `proposal launch is inactive`, `proposal launch replaced or session differs` or `that question is no longer open: … it belongs to an earlier chat`. The CHAT pane or MARSHAL CHAT strip says `not bound` or `restarting`.

### Why

Every Marshal chat is bound to the run it was started for. A restarted chat is a new launch, and an answer from a launch that is no longer bound authorizes nothing.

### How to Fix It

- **0.3.5 or newer**: a restarted or resumed Marshal chat is rebound to the run by itself, and open requests carry over. Activity says `Marshal chat restarted and rebound to this project: N open requests re-sent, …`, and the CHAT pane shows `restarts` and `rebound`. An answer the old chat gave but the Board had not applied yet is asked again under the same request.
- **If it stays `not bound`**, or on an older version: type `/quit` in the control centre and run `marshal` again in the project. The Board starts a new chat bound to the run; open decisions come back in the inbox.

## The Architecture Opened but No Browser Appeared

### Symptom

You pressed `a` (or `y`) on the Accept architecture card and nothing opened, for example over SSH or on a server without a display. The readiness list says `Browser · no display: links are shown on the Board`.

### How to Fix It

The card shows the review's private link:

```text
│ No display here, so open this on the machine:  http://127.0.0.1:37667/?token=7964…462b               │
```

1. Press `c` on the card to copy the link (it uses your terminal's clipboard, which works over SSH where the terminal supports it). The link is kept in memory only, never in a file: it carries the key that accepts the architecture, so nothing else running on the machine can read it from disk.
2. The address is local to the machine Marshal runs on. Over SSH, forward its port first, for example `ssh -L 37667:127.0.0.1:37667 you@server` with the port from the link, then open the link in your local browser.
3. Accept the architecture in the browser. The Board moves on by itself.

## Antigravity (`agy`) Is Not Signed In

### Symptom

The readiness list shows `Workers · agy is installed but not signed in` with the hint `run agy once in a terminal and sign in`, or a run pauses with **Paused · agy not signed in**.

### How to Fix It

1. On the readiness screen press `g`: agy opens in a new tmux window in the project; sign in there and close it. (Or run `agy` once in any terminal.)
2. Back on the Board, press `Enter` (or `v` and then `Enter`) to check it again (one model call). The row turns to `ready`.
3. If a run paused for it, press `Enter` on the paused card to retry, or let the card move the tasks to another provider.

The same applies to the other providers: run `claude` and sign in, run `codex login`, or run `opencode auth login`.
