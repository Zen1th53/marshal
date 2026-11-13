# How a Run Works

Marshal coordinates multiple AI coding agents to complete software engineering goals reliably. Instead of relying on one model to plan, code, review, and test everything, Marshal divides responsibility into a five-stage lifecycle:

```text
[Goal] ──> [1. Plan] ──> [2. Workers] ──> [3. Cross-Review] ──> [4. Independent Verifier] ──> [5. Close to Main]
```

> [!WARNING]
> **Host Execution Notice**: Worker processes execute directly on your host machine under your user account without a sandbox. Worktrees isolate Git changes, not system calls or process execution. The Marshal plans and approves the work itself; accepting the architecture is your one gate, so review it before you accept, and stop the run at any time with `Ctrl+X`.

---

## 1. Plan (Drafting Phase)

A run begins when you provide a goal—either by pressing `s` on the Board or typing into the docked chat pane. The Marshal fills a requirements card; press `Enter` to confirm all, or `e` to edit an answer.

1. **Clarification**: The model acting as the **Marshal** analyzes your repository structure, asks clarifying questions if requirements are ambiguous, and aligns on technical constraints.
2. **Architecture Contract**: The Marshal drafts an architectural proposal and project contract. A `NEEDS YOU` gate appears on the Board; press `y` to open the architecture in your browser and accept it there. This is the owner gate.
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

To prevent models from overlooking their own mistakes, Marshal enforces **cross-provider review**:

- **No Self-Grading**: A worker's hand-in is never reviewed by the same provider that wrote the code. If Codex implemented a task, Claude Code or Antigravity conducts the review.
- **Criteria Verification**: The reviewer inspects the Git diff against the plan's acceptance criteria, checking code quality, missing edge cases, and adherence to project invariants.
- **Rework Loop**: If the reviewer finds defects or unmet criteria, the task is marked `returned` (`r`) and sent back to the worker with specific review comments. If criteria are met, the task is marked `accepted`.

---

## 4. Final Independent Check

After the tasks finish, Marshal prepares for integration:

- **Final check**: An independent check evaluates the integrated solution and reruns its checks.
- **Claude Code requirement**: The final ULTRA check requires Claude Code installed and not used as a worker in that run. If unavailable, the run stops before the check and tells you to install Claude Code or wait for its limit, then press `Enter`.
- **Objective Proof**: Completion is based on real execution status, process exit codes, and test outputs, not on model self-assertions.

---

## 5. Close to Main (Merging & Closed)

When the independent verifier confirms that all tests and criteria pass:

1. **Merge**: Marshal integrates the task branches cleanly into your repository's `main` branch.
2. **State Finalization**: Run metrics, completion proofs, and task history are durably written to `.marshal/state.db`.
3. **Closure**: The run closes to `main`; the chat receives `[Marshal] The run closed: …`.
