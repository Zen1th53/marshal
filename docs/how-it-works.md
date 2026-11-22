# How a Run Works

Marshal coordinates multiple AI coding agents to complete software engineering goals reliably. Instead of relying on one model to plan, code, review, and test everything, Marshal divides responsibility into a five-stage lifecycle:

```text
[Goal] ──> [1. Plan] ──> [2. Workers] ──> [3. Cross-Review] ──> [4. Independent Verifier] ──> [5. Close to Main]
```

> [!WARNING]
> **Host Execution Notice**: Worker processes execute directly on your host machine under your user account without a sandbox. Worktrees isolate Git changes, not system calls or process execution. The Marshal plans and approves the work itself; accepting the architecture is your one gate, so review it before you accept, and stop the run at any time with `Ctrl+X`.

---

## 1. Plan (Drafting Phase)

A run begins when you provide a goal—either by pressing `s` on the Board or typing into the docked chat pane. The Marshal fills a requirements card; confirm all on the Web Board or with `y` on the Board, or press `e` and a line's letter to edit an answer.

1. **Clarification**: The model acting as the **Marshal** analyzes your repository structure, asks clarifying questions if requirements are ambiguous, and aligns on technical constraints.
2. **Architecture Contract**: The Marshal drafts an architectural proposal and project contract. The **Accept architecture** card opens on the Board (`YOUR TURN`); press `a` (or `y`) to open the architecture in your browser and accept it there. Without a browser, the card shows the review's link. This is the owner gate.
3. **Task Decomposition**: The Marshal breaks the architecture down into small, discrete tasks. Each task specifies:
   - Target files or directories.
   - Assigned worker provider.
   - Objective acceptance criteria.
   - Executable verification checks (e.g., unit test commands, linter runs).
4. **Plan Approval**: The Marshal checks and approves its own plan; there is no plan approval popup. You can read the plan on view `7` after a run starts and stop workers at any time with `Ctrl+X`.

---

## 2. Workers (Dispatching Phase)

Once approved, tasks are dispatched to assigned worker agents (such as Claude Code, OpenAI Codex, OpenCode, or Antigravity):

- **Git Worktree Isolation**: Each task executes in its own dedicated Git worktree on a unique task branch. This ensures agents never conflict or overwrite each other's work while running in parallel.
- **Scope Enforcement**: Workers are assigned specific files or directories and tasked with fulfilling the acceptance criteria.
- **Hand-in**: When the worker completes its code edits, it submits a hand-in containing commit digests, modified files, and initial check results.

---

## 3. Cross-Provider Review (Reviewing Phase)

To prevent models from overlooking their own mistakes, every hand-in is reviewed by **another agent with a fresh context**:

- **No Self-Grading**: The reviewer runs in a session of its own, started clean: it shares no conversation with the worker and never resumes the worker's session. A provider other than the worker's is preferred (if Codex implemented a task, Claude Code or Antigravity reviews it). When no other provider is available, the worker's own provider reviews in a new session, and the review records that fallback and its session.
- **Criteria Verification**: The reviewer inspects the Git diff against the plan's acceptance criteria, checking code quality, missing edge cases, and adherence to project invariants.
- **Rework Loop**: If the reviewer finds defects or unmet criteria, the task is marked `returned` (`r`) and sent back to the worker with specific review comments. If criteria are met, the task is marked `accepted`.

---

## 4. Final Independent Check

After the tasks finish, Marshal prepares for integration:

- **Final check**: An independent check evaluates the integrated solution and reruns its checks.
- **A fresh session, not necessarily another provider**: The checker runs in a new agent session with no shared conversation and no worker's session resumed. Marshal prefers a provider that did none of the run's tasks; when none can take the role, a provider that did tasks checks them in a fresh session, and the evidence records that session and the fallback. A checker that reuses a worker's session is refused.
- **Claude Code requirement**: The checker must also be installed, reach Marshal's tools, not be limited, and name the model that answered. Today that is Claude Code, even when it did some of the run's tasks. If it is unavailable, the run stops before the check and tells you to install Claude Code or wait for its limit, then continue with `y` (or the Web Board's button).
- **Objective Proof**: Completion is based on real execution status, process exit codes, and test outputs, not on model self-assertions.

---

## 5. Close to Main (Merging & Closed)

When the independent verifier confirms that all tests and criteria pass:

1. **Merge**: Marshal integrates the task branches cleanly into your repository's `main` branch.
2. **State Finalization**: Run metrics, completion proofs, and task history are durably written to `.marshal/state.db`.
3. **Closure**: The run closes to `main`; the chat receives `[Marshal] The run closed: …`.

## Architecture lifecycle

The project has one living architecture. For its baseline, the Marshal describes the whole project, with all parts marked new. Each later goal proposes the next revision: the Marshal keeps existing parts, describes what each connection carries, and ties the requirements to components.

Press **`a`** on the Board, or use **`/marshal architecture`**, to open the current architecture at any time. **`y`** opens a proposal waiting for your acceptance. The browser header shows `rev N → N+1 · proposed` while waiting and `rev N · current` after acceptance.

Solid borders show current parts. Dashed borders and edges show additions connected to the current architecture. Changed parts have a dashed accent outline and a “changed” tag; open a part or connection to see its before and after fields. Removed parts are faded and struck through. Use **Only this goal’s changes** to focus on the proposal while keeping connected existing parts as context.

Scroll or pinch to zoom and drag to pan. Click a part that has something inside (it shows "N parts inside", or, with **Advanced view** on, any part, whose inside is then derived from its code) to open it: the drawing zooms into it, the breadcrumb and the page address follow, and that part's panel (description, covers, incoming and outgoing links) stays open beside its inside. The pointer turns into a zoom-in cursor and the tooltip says "Click to open" on such parts. Click a part with nothing inside to focus it: it and its neighbours stay bright and its panel opens. With the keyboard, `Enter` opens a part and `Space` focuses it; `Backspace`, the breadcrumb or the browser's Back button return. A drag that pans never opens a part, and **Select area** and the pencil work as before.

To ask the Marshal about something, use the pencil, Shift-click (or Alt-click) a part or connection, or **Select area**. This works on every level, including the levels derived from code in Advanced view: packages, files, and the connections between them. For one file you can also name a declaration (`Runtime.Open`). The request then carries the exact place, by breadcrumb, kind and repository path, and is marked "derived from code, not reviewed". The server checks that every path is one drawn on that level, inside the project and not a symbolic link. A request about derived code never changes the architecture and never affects **Accept**: the Marshal only answers it. Open requests show as a dot on the element, and **History** lists every request with its path.

Comment on the drawing, choose builders if available, and select **Accept architecture** when ready. The Marshal then plans the work and continues the run. **History** lists revisions with their date, goal, run, and who accepted them. Select a revision to inspect its drawing and changes against the preceding revision; return to the latest architecture to comment or accept.

After a run, components whose paths were changed can show **built**. A failed conformance check marks them **differs from the architecture**, so you can see where the implementation needs attention. These marks appear when the run supplies conformance results.
