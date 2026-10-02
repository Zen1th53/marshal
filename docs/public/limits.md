# What MARSHAL cannot do yet

MARSHAL 0.0.5 is an early version. Knowing its limits helps you use it safely.

**It has not yet been tested end to end with real AI models.**
Each step is covered by automated tests that use simulated agents. A full run
from the first conversation to accepted work has not been completed with real
models yet. Try it on a small, unimportant change first.

**The window must stay open while work runs.**
MARSHAL has no background service. If you close the window, the work stops.
Use `/marshal resume` to continue.

**It runs on Linux only.**
macOS is not supported yet. On Windows you can try WSL 2, which has not been
tested.

**OpenCode cannot be the Marshal.**
It can do tasks as a worker, but planning needs Codex, Claude Code or
Antigravity.

**Scope checks only warn.**
MARSHAL notices when an agent changes files outside its task or drifts from
the goal, and records it, but it does not stop the task. You see the warning
when you review.

**Some agent versions are not checked.**
MARSHAL has checked its commands only against specific versions. See
[Connect your AI agents](agents.md).

**Agents that need the internet can be blocked.**
If MARSHAL cannot control an agent's network access safely, it may refuse to
run the task.

**Restoring a backup from inside MARSHAL works on Linux only.**
On other systems, restore from the terminal instead.

**Some screens are not available yet.**
The navigation screens (Ctrl+N) are switched off in this version. Everything
is available through commands in the composer.
