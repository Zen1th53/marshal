# Pause, stop and set limits

## Choose the visible window

MARSHAL opens on the control centre (window 0), including after re-attach or automatic workspace setup. The Marshal chat stays open in its own window and automatically restarts or resumes without taking focus. Worker windows remain available in the background.

Press **F7** for Codex, **F8** for Claude Code, **F9** for OpenCode or **F12** for Antigravity. Use `/view show chat` for the Marshal chat, or `/view` and `/takeover` to choose an agent view. `/focus` returns to the control centre. Press **F11** from any MARSHAL window to return to the control centre.

Native sessions you open with F7/F8/F9/F12, `/codex`, `/claude`, `/opencode`,
`/agy`, or `/<agent> new|continue` accept keyboard input immediately. The Marshal
chat also accepts input. Workers launched by the Marshal or a governed or
automated run open view-only; use `/takeover` to type into a worker pane.

Agent starts, alerts and worker completion keep your current window selected. Automatic following is off until you enable it with `/view follow`; use the same command to turn it off.

## See what is running

```text
/marshal status
/status
```

`/marshal status` shows the current plan and its tasks. `/status` shows the
whole session.

## Stop and continue a planned run

```text
/marshal stop
/marshal resume
```

`/marshal stop` stops the run and keeps everything as it is. `/marshal resume`
continues from where it stopped, including after you closed and reopened
MARSHAL.

## Pause, resume or cancel other runs

Work started outside a plan, for example with `/codex exec`, is a **run** with
its own ID.

```text
/pause run:RUN-ID
/resume run:RUN-ID
/cancel run:RUN-ID
```

- **Pause** stops new tasks from starting. A task that is already running
  finishes its current step first.
- **Resume** continues a paused run.
- **Cancel** stops the run for good.

You can leave out the ID. If exactly one run fits, MARSHAL acts on it. If
there are several, it lists them with their IDs so you can choose.

After pausing or cancelling, check `/status` again: an agent can take a moment
to finish what it was doing.

## Set limits

You can limit how many AI calls a run may make and how long it may take.
Limits belong to your current goal:

```text
/budget set calls=20 duration=30m
```

MARSHAL asks you to confirm the change. It shows a confirmation ID; approve it
with:

```text
/approve CONFIRMATION-ID
```

To see your limits and how much has been used:

```text
/budget
```

To remove the limits:

```text
/budget clear
```

When a limit is reached, MARSHAL does not start the next task. It does not cut
off a task in the middle.

!!! note "Why does it say UNKNOWN?"
    Not every agent reports how many tokens it used or what it cost, so
    MARSHAL cannot limit tokens or money. When a number is not available,
    MARSHAL shows `UNKNOWN` rather than guessing.

## Allow or revoke a worker's network destination

A governed worker starts with access to its provider endpoint only. When another
destination is refused, review the permission request and press uppercase **A**
to allow it. Every other key, closing the request, or timeout denies it.

You can also use:

```text
/egress status
/egress allow RUN-ID example.com:443
/egress revoke RUN-ID example.com:443
```

Status lists active runs started by the TUI and by `marshal run` through the
local daemon. A grant reaches the runtime running that worker. It applies only
to that run and exact host and port; omitting the port means 443. The worker must
retry a refused request. A model cannot grant access. Grants expire when the run
ends; completed runs appear only in the refusal evidence inbox.

Revoking denies the next request and closes existing connections. For a worker
owned by another runtime, closing existing connections happens on the next
100 ms poll, subject to scheduling and storage latency.
