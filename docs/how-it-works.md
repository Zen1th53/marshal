# How a Run Works

Marshal coordinates multiple AI coding agents to complete software engineering goals reliably. Instead of relying on one model to plan, code, review, and test everything, Marshal divides responsibility into a five-stage lifecycle:

```text
[Goal] ──> [1. Plan] ──> [2. Workers] ──> [3. Cross-Review] ──> [4. Independent Verifier] ──> [5. Close to Main]
```

> [!WARNING]
> **Host Execution Notice**: Worker processes execute directly on your host machine under your user account without a sandbox. Worktrees isolate Git changes, not system calls or process execution. Always review goals, plans, and generated code before approving work.

---

## 1. Plan (Drafting Phase)

A run begins when you provide a goal—either by pressing `s` on the Board or typing into the docked chat pane.

1. **Clarification**: The model acting as the **Marshal** analyzes your repository structure, asks clarifying questions if requirements are ambiguous, and aligns on technical constraints.
2. **Architecture Contract**: The Marshal drafts an architectural proposal and project contract defining components, invariants, and file paths. A `NEEDS YOU` gate appears on the Board for you to review and accept (`y`, then `Enter`; or type `accept architecture` in the chat).
3. **Task Decomposition**: The Marshal breaks the architecture down into small, discrete tasks. Each task specifies:
   - Target files or directories.
   - Assigned worker provider.
   - Objective acceptance criteria.
   - Executable verification checks (e.g., unit test commands, linter runs).
4. **Plan Approval**: The Marshal checks the drafted task graph and approves it itself. You can read it on the Plan view (`7`). If the Marshal asks you instead, press `y` and then `Enter` on the `NEEDS YOU` gate.

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

## 4. Independent Verifier (Verifying Phase)

After all individual tasks have been accepted, Marshal prepares for integration:

- **Independent Verification**: An independent verifier model—selected from providers other than the worker providers that authored changes—evaluates the integrated solution.
- **Check Execution**: The verifier runs the project's actual build and test commands (e.g. `go test ./...`, `pytest`, `cargo test`) on your computer, against the integrated result.
- **Objective Proof**: Completion is based on real execution status, process exit codes, and test outputs, not on model self-assertions.

---

## 5. Close to Main (Merging & Closed)

When the independent verifier confirms that all tests and criteria pass:

1. **Merge**: Marshal integrates the task branches cleanly into your repository's `main` branch.
2. **State Finalization**: Run metrics, completion proofs, and task history are durably written to `.marshal/state.db`.
3. **Closure**: The run state transitions to `CLOSED`. You have verified code in your main branch with a full Git commit history.
