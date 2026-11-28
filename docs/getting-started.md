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

Opening `marshal` in a Git repository that is not set up first shows what setup will do and asks: the files it commits as `Initialize MARSHAL project` and on which branch, the commit workers start from, how many uncommitted files they will not see, that workers run as you without a sandbox, and whether anonymous telemetry is on (you can turn it off there). Answer `n` and nothing is changed; run `marshal` again or `marshal init` when you are ready. With no terminal to ask on, opening stops with that explanation; run `marshal init` or set `MARSHAL_ACCEPT_SETUP=1` to accept. In a folder outside a Git repository, Marshal can create a project repository for that folder; it leaves files that look like secrets untracked. It will not use your home folder or `/` itself as a project.

---

## 2. Open the Board and set up the project

From your project root, open Marshal:

```bash
marshal
```

On first open, Marshal asks before it sets up the project (see above). You can also run `marshal init` yourself as an optional explicit setup command. Setup:
- Creates the local state directory at `.marshal/`, where runtime databases and task logs reside.
- Writes initial project configuration files: `CAPABILITIES.yaml`, `PACK-VERSION.yaml`, and `RUNTIME-VERSION.yaml`.
- Configures Git to ignore the `.marshal/` runtime state using `.git/info/exclude`, leaving your personal `.gitignore` clean and untouched.
- Commits initial configuration defaults to Git.

---

Marshal opens inside a managed tmux environment displaying **the Board**, and the **Web Board** (the full interface) opens in your browser. On a computer with no display, `/marshal web` shows its private link to open. To open the Board in another browser, `/marshal web copy` copies a fresh one-use link to the clipboard (with `pbcopy` on macOS).

## 3. Choose who plans and check readiness

On the first open the Board asks **Who plans?**: which provider runs the Marshal. Each provider is listed with what is left of its limit:

```text
Who plans?  ⬡ codex 11% left · ✳ claude limit unknown · ◆ agy out of limit until 09:32
[Enter] keep codex · [↑↓] or click to change · [1-3] pick
```

Press `Enter` to keep the suggestion, or choose another with the arrows or its number. You can change it later with `m`.

The Board then shows its home screen, with `READY` on the top line:

```text
 MARSHAL · my-project · READY · 0 workers · 18:22

 Now:       the Marshal is waiting for a goal
 From you:  nothing · next ask: architecture review
 Web:       opened in your browser (w opens it again)
 ⏎ chat   w web   s say to the Marshal   q quit
```

The readiness checklist (the Marshal's provider, the workers, the platform, the repository, the browser) is on the Web Board. If something needs you there:

- **A provider that is not signed in**: for agy, *From you* says so and `g` opens agy in a new tmux window to sign in (or run `agy` in any terminal). For Claude Code run `claude` and sign in; for Codex run `codex login`; for OpenCode run `opencode auth login`.
- **A provider that is not checked yet**: open the Providers view (`4`), press `v`, then `Enter`. The check costs one model call.
- **Repo** with changes: workers start from the last commit, so commit what they should see.

On a first open, *From you* says that anonymous usage counts are on (install and version counts to marshal.blackhat.uz, never code, prompts or file names). Press `y` to keep them on or `t` to turn them off; `marshal telemetry disable` turns them off later. [Exactly what is sent →](telemetry.md)

At least one provider has to be ready. With two or more, a different provider reviews each task; with one, it reviews in a fresh session of its own.

---

## 4. Say the goal

To start work, describe what you want built:

- **From the Board**: press `s`. The composer opens at the bottom of the Board (`to the Marshal › …`). Type your goal and press `Enter`.
- **In the chat**: press `Enter` (or `F11`) to open the Marshal chat beside the Board. Type your goal there. `F11` or `Alt+→` brings the keyboard back to the Board.

Example goal:
> "Add a health check HTTP endpoint at /healthz that returns JSON with status ok and current server uptime. Include unit tests."

The top line changes to `PLANNING` and *Now* says what the Marshal is doing. The Web Board shows the plan stages, the **Marshal steps** read from its tool calls, the requests the Board sent the Marshal, and the state of the Marshal chat. When the Marshal asks you something in its chat, *From you* says so: `Enter` opens the chat.

---

## 5. Confirm the requirements and accept the architecture

The Marshal reads your repository, asks questions only where the answer changes the plan, and fills a **requirements card** on the Web Board. Confirm all answers there, or press `y` on the Board (not `Enter`, which opens the chat); `e` then a line's letter edits one line. The *From you* line shows both keys: `requirements … — y confirm all · e edits a line`.

Then it drafts the architecture. The top line says `YOUR TURN` and *From you* says `architecture waits for you in the browser — open the web to decide (w)`. On the Web Board's **Accept architecture** card:

1. Select **Open in browser** (or press `a` on the Board). The architecture opens in your browser.
2. Review it and select **Accept architecture** there. Only you can accept, and only in the browser: if the Marshal tries to accept for you, the card says it was refused. **Accepting starts the work**: the Marshal writes the task plan, approves it itself and starts the workers; you are not asked again before they start (`Ctrl+X` stops them).
3. To ask for changes, comment in the browser, or tell the Marshal (`s`) what to change.

**No browser here (SSH, a server without a display):** the Web Board's card shows the review's link. The link is kept in memory only, never in a file. Over SSH, the address is local to the remote machine: forward its port first (for example `ssh -L 37667:127.0.0.1:37667 you@server`, with the port from the link), then open the link in your local browser.

The accepted architecture becomes the binding contract for all subsequent tasks.

---

## 6. The Marshal plans and approves the plan

Next, the Marshal decomposes the architecture into discrete, small tasks. Each task defines:
- Assigned worker model (e.g. Codex, Claude, OpenCode, Antigravity).
- Scope of files to create or modify.
- Concrete check criteria (e.g. `go test -v ./...` or `npm test`).

In the shipped build the Marshal approves the plan itself and starts the workers; there is no plan approval popup. Press `7` to read the plan once the run exists. You can stop the workers at any time with `Ctrl+X`.

---

## 7. Watch the lanes

Once the plan is approved the top line says `RUNNING`, *Now* counts the tasks running, queued and done, and the Web Board shows the run: the plan, one lane per worker from its heartbeats, every task with its writer, reviewer and state, the Marshal and each provider's limit, the inbox and the activity.

What happens on its own:

1. **Dispatch**: each task runs in a separate Git worktree, so workers never overwrite each other's changes.
2. **Execution**: the worker implements the changes and hands them in.
3. **Review**: for every plan, another agent with a fresh context (a different provider when one is available) checks the diff against the task's criteria. If it finds problems, the task goes back for rework.
4. **Final check**: an independent check, in a fresh session of its own, reruns the checks on the integrated result. The final check accepts any qualified configured provider in a fresh session, with native model identity and exact commit binding. The owner can pin a family with `/marshal settings final-provider <any|codex|claude|opencode|agy>`. If none qualifies, the run pauses with the reason until a provider becomes available.

To look closer, open the Web Board (`w`). If a task gets stuck, *From you* names it: `y` retries it in a fresh session (the Web Board's **Retry** does the same). `/task-model T03` changes the model a task runs on. `Ctrl+X` stops all workers.

---

## 8. When work pauses: the recovery card

If workers stop (a provider hits its limit, is not signed in, a lane was interrupted, an operation timed out), the top line says `PAUSED` and *From you* says what to do, with its key:

```text
 From you:  run paused: agy Gemini 5h limit — y move T06 and T09 to claude
```

- `y` does what the paused card's main button says (move the tasks to a provider with room, retry them in fresh sessions, or resume), once.
- On the Web Board's paused card: **Wait until …** waits for the limit reset, **Raw error** shows it, **Stop the run** stops it (the work is kept and `/marshal resume` continues it).

---

## 9. Result closed to main

When all tasks pass review and verification:
- Marshal merges the task branches cleanly into your project's `main` branch.
- The top line says `DONE`.
- The chat receives a `[Marshal] The run closed: …` message.
- You can review the completed commits with standard Git commands (`git log`, `git diff HEAD~1`).
- Press `q`, `Ctrl+C` twice, or type `/exit` in the command line to leave the Board. While work still runs, or another terminal shows the Board, `q` asks first: `Enter` detaches this terminal and everything keeps running (run `marshal` to return), `s` stops everything. Closing the terminal window also leaves Marshal running in the background.

All keys and screens: [The Board and keys](board-and-keys.md).
