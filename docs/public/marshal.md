# Marshal mode

The Marshal plans with you, then coordinates workers. It can use Codex, Claude
or Antigravity as the planning model. OpenCode can be a worker.

## Chat and plan pack

```text
/marshal model claude
/marshal settings
/marshal chat
```

Describe the outcome, files in scope, constraints and acceptance checks. The
Marshal examines the project and asks questions before preparing the plan.
The plan pack contains `REQUIREMENTS.md`, `00_INDEX.md` and a task note per task.
After leaving the conversation with `/exit`, read the location MARSHAL prints.
Read and correct the pack before approving it. Approval binds the pack as it
stands; workers receive the requirements and their task note.

## Choose how work is controlled

Settings apply to the next run. Set them before drafting that run:

```text
/marshal settings control strict
/marshal settings acceptance-mode marshal-then-user
```

`strict` asks workers to follow the task instructions exactly; `free` lets them
choose an approach within the task's files. These are instructions, not a
promise that an agent cannot deviate. Alignment checks remain advisory.

Acceptance modes are `marshal` (Marshal acceptance), `user` (your acceptance),
and `marshal-then-user` (Marshal review followed by your decision).
The last is the default.

## Approve and watch

```text
/marshal approve
/marshal status
```

Approval starts the drafted plan. Workers run tasks in separate worktrees;
dependent tasks start from the merged work they depend on. MARSHAL runs the
task's checks again. Review and verification roles use separate sessions;
one installed provider can fill multiple roles.

If a task pauses for your decision, inspect the request and use
`/marshal approve-task <approval-id>` with the displayed identifier.
Approving a plan does not pre-authorize every later hard approval.

## Review, return and accept

Inspect the task checks and reported evidence, then inspect changes with `/diff`.
In `user` or `marshal-then-user` mode, use the task identifier shown in status:

```text
/marshal return TASK-ID Add the missing regression check
/marshal resume
/marshal accept TASK-ID
/marshal resume
```

Return applies to a task awaiting your decision and sends it back for rework.
The reply tells you to resume. Accept only after the task satisfies your criteria. Acceptance authorizes that
task once; resume to continue the run.
When the run is verified and its required decisions are complete,
`/marshal close` closes it and moves the target branch.

Use `/marshal amend <reason>` to request a plan change. Major amendments require
`/marshal amend approve` or `/marshal amend deny`. `/marshal stop` keeps run state;
`/marshal resume` continues a stopped or interrupted run.

Read [known limits](concepts.md) before relying on an unattended run.
