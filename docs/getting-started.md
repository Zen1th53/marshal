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

## 3. Board Layout

The Board is divided into clean operational sections:
- **Vitals row**: Shows provider limits and host vitals on every screen.
- **Header**: Displays run status and `NEEDS YOU` gates.
- **Docked Chat (left)**: Conversation pane with the Marshal model.
- **Tasks Pane (center)**: Current tasks, their status (`queued`, `dispatched`, `reviewing`, `merged`), and assigned workers.
- **Lanes Pane (right)**: Live status of active worker processes.
- **Activity Log**: Event stream of state transitions, reviews, and verifications.
- **Footer**: Key bindings and quick shortcuts.

---

## 4. Give a Goal with `s` or in the Chat

To start work, describe what you want built:

- **Quick input from anywhere**: Press `s` on your keyboard. This opens the "Say" composer directly over the Board. Type your goal and press `Enter`.
- **In the chat pane**: Press `F11` or `Alt+←` to move keyboard focus to the docked chat pane. Type your goal into the conversation and press `Enter`.

Example goal:
> "Add a health check HTTP endpoint at /healthz that returns JSON with status ok and current server uptime. Include unit tests."

---

## 5. Accept the Architecture in the Browser

After receiving your goal, the Marshal reviews your repository structure, asks clarifying questions if needed, and drafts an **architecture proposal** and project contract.

When the draft is ready:
1. The Marshal fills a requirements card. Press `Enter` to confirm all answers, or `e` to edit one.
2. The Board header alerts with **`NEEDS YOU`** for the architecture:
   ```text
   ⚑ architecture · waits for you in the browser
       [y] open in browser
   ```
3. Press `y` on the Board. The architecture opens in your browser.
4. Review it and accept it there. The Marshal moves on by itself; no key is needed.

The accepted architecture becomes the binding contract for all subsequent tasks.

---

## 6. The Marshal Approves the Plan

Next, the Marshal decomposes the architecture into discrete, small tasks. Each task defines:
- Assigned worker model (e.g. Codex, Claude, OpenCode, Antigravity).
- Scope of files to create or modify.
- Concrete check criteria (e.g. `go test -v ./...` or `npm test`).

When the plan is ready, the Marshal approves it itself and starts the workers; there is no plan approval popup. Press `7` to read the plan after a run exists. You can stop the run at any time with `Ctrl+X`.

---

## 7. Watch the Run

Once the plan is approved, Marshal runs the work automatically:

1. **Dispatch**: Tasks are assigned to worker agents. Each task runs in a separate Git worktree so workers never overwrite each other's in-progress changes.
2. **Execution**: The worker implements the changes and submits them for review.
3. **Cross-Provider Review**: A different provider inspects the diff against the task's criteria. If issues are found, the work is returned for rework (`r`).
4. **Final check**: An independent check reruns the checks against the integrated result. This final ULTRA check needs Claude Code installed and unused as a worker in that run. Otherwise the run stops before the check and asks you to install Claude Code or wait for its limit, then press `Enter`.

You can inspect any running or completed task at any time:
- Highlight a task with `j` / `k` (or `↑` / `↓`) and press `Enter` to open its **Inspector**.
- Press `o` inside the inspector to view the live worker terminal pane.

---

## 8. Result Closed to Main

When all tasks pass review and verification:
- Marshal merges the task branches cleanly into your project's `main` branch.
- The run state transitions to `CLOSED`.
- The chat receives a `[Marshal] The run closed: …` message.
- You can review the completed commits with standard Git commands (`git log`, `git diff HEAD~1`).
- Press `Ctrl+C` twice or type `/exit` in the command line to leave the Board.
