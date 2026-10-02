# Your first project

In this walkthrough you ask MARSHAL for a small change, approve the plan,
let an agent do the work, and accept the result. Pick something small for the
first try, such as "add a test for the `parse_date` function".

Before you start, finish [Install MARSHAL](install.md) and make sure at least
one AI agent is installed and signed in.

## 1. Open MARSHAL

In your project folder, run:

```bash
marshal tui
```

The MARSHAL window opens. At the bottom is the **composer**, where you type
commands. Type `/help` at any time to see what you can do.

!!! tip
    Typing plain text in the composer does nothing on its own. Every action
    starts with a command such as `/marshal chat`.

## 2. Choose who plans

Tell MARSHAL which AI should be the Marshal. Use the agent you have installed:

```text
/marshal model codex
```

You can use `codex`, `claude` or `agy`.

## 3. Talk through your goal

Start the planning conversation:

```text
/marshal chat
```

The Marshal introduces itself, asks which language you want to use, and asks
what you want to achieve. Answer in plain words. It then looks at your project
and asks questions one at a time, each with a suggested answer.

When you have answered everything, ask it to write the plan. It creates the
**plan pack**: the requirements, a list of tasks, and a note for each task.

When you are done, type `/exit` to leave the conversation. MARSHAL shows you
where the plan was saved.

## 4. Read the plan

Open the plan files in your editor. Check that:

- the requirements say what you actually want,
- each task is small and clear,
- each task says how to check that it is done.

If something is wrong, edit the files directly. Your approval covers the plan
as it is when you approve it.

## 5. Approve and let the agents work

```text
/marshal approve
```

The agents start working. Each task runs in its own separate copy of your
project. To see how the work is going:

```text
/marshal status
```

!!! warning "Keep the MARSHAL window open"
    The work stops if you close the window. If that happens, open MARSHAL
    again and use `/marshal resume`.

Sometimes an agent needs your permission for a step. MARSHAL then shows a
request with an ID. Read what the agent wants to do, and if you agree:

```text
/marshal approve-task APPROVAL-ID
```

Replace `APPROVAL-ID` with the ID MARSHAL shows you.

## 6. Review the result

When a task is finished, MARSHAL runs its checks again and asks for your
decision. Look at what changed:

```text
/diff
```

If you are happy with a task, accept it. If not, send it back with a short
note saying what to fix. Use the task ID shown by `/marshal status`:

=== "Accept"

    ```text
    /marshal accept TASK-ID
    /marshal resume
    ```

=== "Send back"

    ```text
    /marshal return TASK-ID The test does not cover empty input
    /marshal resume
    ```

`/marshal resume` lets the run continue after your decision.

## 7. Finish

When every task is done and accepted:

```text
/marshal close
```

This closes the run and brings the finished work into your project.

## What next?

- Change how strictly agents follow the plan, or who reviews the work:
  [Settings](settings.md).
- Undo something you did not want: [Undo changes and keep backups](undo.md).
- Stop or pause work, or set limits: [Pause, stop and set limits](control.md).
