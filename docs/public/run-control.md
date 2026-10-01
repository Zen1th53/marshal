# Run control and budgets

Use `/status`, `/marshal status` and `/budget` to inspect work before acting.
For governed runs in this session:

```text
/pause run:RUN-ID
/resume run:RUN-ID
/cancel run:RUN-ID
```

Use the ID the session displays. Without an ID, pause or cancel acts only when
exactly one run qualifies. Pause stops new dispatch at a task boundary; a provider
turn can still be settling. Resume checks the goal is still current. Cancel
stops further dispatch and requests cancellation of supervised provider turns.
Stored cancellation intent is not proof a process has exited.

`/marshal stop` and `/marshal resume` control the current Marshal run.
Bare `/resume` controls a governed run; `/resume --last` resumes native Codex.

## Enforced limits

An active goal is required:

```text
/budget set calls=20 duration=30m
/approvals
```

Changing limits revises the goal and leaves it pending. Confirm the exact typed
ID returned with `/approve <id>`. `/budget clear` also creates a pending revision.
A changed goal can invalidate an older run's approval; do not assume that
resuming an old run applies new limits to it.

The engine checks model calls and duration per run before dispatching another
task. Reaching a limit blocks further dispatch. It is not a mid-turn timer.
Token and cost limits are refused because not every provider reports them.
Missing measurements appear as `UNKNOWN`, not zero.

Inspect [approvals and limits](concepts.md) when a run is blocked.
