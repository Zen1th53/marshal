# Getting Started

This guide walks you through your first complete multi-agent engineering run with Marshal: from a fresh Git repository to merged code in `main`.

> [!WARNING]
> **Host Execution Notice**: Workers execute on your local machine with your user privileges and without a sandbox. Worktrees isolate Git changes, not operating system calls. The Marshal plans and approves the work itself; accepting the architecture is your one gate, so review it before you accept, and stop the run at any time with `Ctrl+X`.

---

## 1. Prepare your Project

Marshal works inside any standard Git repository. Navigate to an existing project or create a fresh one:

```bash
mkdir my-project
cd my-project
git init
git commit --allow-empty -m "Initial commit"
```

A Git repository must have at least one commit so Marshal can establish a clean baseline for worker branches and merges.

Opening `marshal` in a Git repository that is not set up adds the project files and commits them as `Initialize MARSHAL project`. In a folder outside a Git repository, Marshal can create a project repository for that folder; it leaves files that look like secrets untracked. It will not use your home folder or `/` itself as a project.

---

## 2. Open the Board and set up the project

From your project root, open Marshal:

```bash
marshal
```

On first open, Marshal sets up the project automatically. You can also run `marshal init` yourself as an optional explicit setup command. Setup:
- Creates the local state directory at `.marshal/`, where runtime databases and task logs reside.
- Writes initial project configuration files: `CAPABILITIES.yaml`, `PACK-VERSION.yaml`, and `RUNTIME-VERSION.yaml`.
- Configures Git to ignore the `.marshal/` runtime state using `.git/info/exclude`, leaving your personal `.gitignore` clean and untouched.
- Commits initial configuration defaults to Git.

---

Marshal opens inside a managed tmux environment displaying **the Board**.

## 3. Choose who plans and check readiness

On the first open the Board asks **Who plans?**: which provider runs the Marshal. Each provider is listed with what is left of its limit:

```text
Who plans?  ⬡ codex 11% left · ✳ claude limit unknown · ◆ agy out of limit until 09:32
[Enter] keep codex · [↑↓] or click to change · [1-3] pick
```

Press `Enter` to keep the suggestion, or choose another with the arrows or its number. You can change it later with `m`.

The Board then shows the **readiness** screen, with `READY` in the top bar:

```text
  MARSHAL v0.3.5   project my-project   READY                                     ⬡ codex · GPT-6.2-Sol · high  m   inbox 0  18:22
 NOW  marshal codex GPT-6.2-Sol · ready         │ work none yet                       from you nothing

╭─ READINESS · 5 of 6 ready ────────────────────────────────────────────────────────╮
│                                                                                   │
│  ✓  Marshal    codex · GPT-6.2-Sol · signed in                           in use   │
│  ✓  Workers    claude · up to 2 at once                                   ready   │
│  !  Workers    agy is installed but not signed in                        action   │
│                run agy once in a terminal and sign in                             │
│  ✓  Platform   Linux amd64 · workers can run here                         ready   │
│  ✓  Repo       clean · main @ 16091d7                                     ready   │
│  ✓  Browser    links open in your browser                                 ready   │
```

Go through the rows marked `!`:

- **A provider that is not signed in** (here agy): press `g` to open agy in a new tmux window and sign in there (or run `agy` in any terminal); when you close it, press `Enter` to check it again. For Claude Code run `claude` and sign in; for Codex run `codex login`; for OpenCode run `opencode auth login`.
- **A provider that is not checked yet**: press `v`, then `Enter`. The check costs one model call.
- **Repo** with changes: workers start from the last commit, so commit what they should see.

On a first open, a **USAGE COUNTS** card beside the list says that anonymous install and version counts go to marshal.blackhat.uz, and never code, prompts or file names. Press `y` to keep them on, `t` to turn them off, or `?` to see what is sent; `marshal telemetry disable` turns them off later. [Exactly what is sent →](telemetry.md)

At least one provider has to be ready. With two or more, a different provider reviews each task.

---

## 4. Say the goal

To start work, describe what you want built:

- **From the Board**: press `s`. The composer opens at the bottom of the Board (`to the Marshal › …`). Type your goal and press `Enter`.
- **In the chat**: press `Enter` on the readiness screen (or `F11`) to open the Marshal chat beside the Board. Type your goal there. `F11` or `Alt+→` brings the keyboard back to the Board.

Example goal:
> "Add a health check HTTP endpoint at /healthz that returns JSON with status ok and current server uptime. Include unit tests."

The top bar changes to `PLANNING`. The Board shows the plan stages, the **Marshal steps** read from its tool calls, the requests the Board sent the Marshal, and the state of the Marshal chat.

---

## 5. Confirm the requirements and accept the architecture

The Marshal reads your repository, asks questions only where the answer changes the plan, and fills a **requirements card**. Press `Enter` to confirm all answers, or `e` to edit one.

Then it drafts the architecture. The top bar says `YOUR TURN`, *from you* says `architecture waits for you in the browser`, and the **Accept architecture** card opens over the Board:

```text
╭─ ACCEPT ARCHITECTURE · rev 1 ──────────────────────────────────────────────────────────── esc later ─╮
│ 14 parts · 37 links · covers R1–R18 · 5 parts changed by this goal (dashed)                          │
│   …                                                                                                  │
│  a  open in browser    c  copy link    k  comment                                                    │
╰──────────────────────────────────────────────────────────────────────────────────────────────────────╯
```

1. Press `a` (or `y`). The architecture opens in your browser.
2. Review it and select **Accept architecture** there. Only you can accept, and only in the browser: if the Marshal tries to accept for you, the card says it was refused.
3. To ask for changes, comment in the browser, or press `k` on the card and say what to change.

**No browser here (SSH, a server without a display):** the card shows the review's link instead:

```text
│ No display here, so open this on the machine:  http://127.0.0.1:37667/?token=7964…462b               │
```

Press `c` to copy the link and open it in a browser on that machine. The link is kept in memory only, never in a file. Over SSH, the address is local to the remote machine: forward its port first (for example `ssh -L 37667:127.0.0.1:37667 you@server`, with the port from the link), then open the link in your local browser.

`Esc` puts the card aside; the inbox keeps the decision and *from you* still names it.

The accepted architecture becomes the binding contract for all subsequent tasks.

---

## 6. The Marshal plans and approves the plan

Next, the Marshal decomposes the architecture into discrete, small tasks. Each task defines:
- Assigned worker model (e.g. Codex, Claude, OpenCode, Antigravity).
- Scope of files to create or modify.
- Concrete check criteria (e.g. `go test -v ./...` or `npm test`).

With the default settings the Marshal approves the plan itself and starts the workers; there is no plan approval popup. Press `7` to read the plan once the run exists. You can stop the workers at any time with `Ctrl+X`.

---

## 7. Watch the lanes

Once the plan is approved the top bar says `RUNNING` and the Board shows the run:

- **PLAN**: the stages, `build 5/12`, progress and ETA.
- **LANES**: one row per worker from its heartbeats: model, task or review, step, time, and how long ago it last signalled.
- **TASKS**: each task's files, writer, reviewer and state.
- **MARSHAL** and **WORKERS**: the Marshal's model, context and limit; each provider's limit and lanes.
- **INBOX** and **ACTIVITY**: what waits for you, and what happened.

What happens on its own:

1. **Dispatch**: each task runs in a separate Git worktree, so workers never overwrite each other's changes.
2. **Execution**: the worker implements the changes and hands them in.
3. **Cross-provider review**: a different provider checks the diff against the task's criteria. If it finds problems, the task goes back for rework.
4. **Final check**: an independent check reruns the checks on the integrated result. This final ULTRA check needs Claude Code installed and unused as a worker in that run. Otherwise the run stops before the check and asks you to install Claude Code or wait for its limit, then press `Enter`.

To look closer:
- Highlight a task with `j` / `k` (or `↑` / `↓`) and press `Enter` to open its **Inspector**; press `o` there to see the worker's terminal pane.
- Focus the lanes with `l` and press `Enter` on a lane to open its worker.
- Press `Space` for the action palette: say to the Marshal, open the architecture, restart a task, change model or provider, pause everything, stop all workers, limits and workers, help.

If a task gets stuck, select it and press `r`, then `Enter`, to restart it.

---

## 8. When work pauses: the recovery card

If workers stop (a provider hits its limit, is not signed in, a lane was interrupted, an operation timed out), the top bar says `PAUSED` and the **paused card** says what happened, which tasks it holds, what is kept, and what `Enter` will do:

```text
╭─ PAUSED · agy Gemini 5h limit ───────────────────────────────────── resets 21:10 ─╮
│ agy-2 and agy-4 hit the agy Gemini 5-hour limit. Their work is kept.              │
│ Enter will: move T06 and T09 to claude sonnet in fresh sessions, keep commits.    │
│  ⏎  move to claude    w  wait until 21:10    d  raw error    x  stop the run      │
╰───────────────────────────────────────────────────────────────────────────────────╯
```

One key gets the run moving again:

- `Enter` does what the card says (move the tasks to a provider with room, retry them in fresh sessions, or resume).
- `w` waits until the limit resets and then resumes.
- `d` shows the raw error.
- `x` stops the run; the work is kept and `/marshal resume` continues it.

---

## 9. Result closed to main

When all tasks pass review and verification:
- Marshal merges the task branches cleanly into your project's `main` branch.
- The top bar says `DONE`.
- The chat receives a `[Marshal] The run closed: …` message.
- You can review the completed commits with standard Git commands (`git log`, `git diff HEAD~1`).
- Press `Ctrl+C` twice or type `/exit` in the command line to leave the Board.

All keys and screens: [The Board and keys](board-and-keys.md).
