# What MARSHAL cannot do yet

MARSHAL 0.0.5 is an early version. Knowing its limits helps you use it safely.

**It has not yet been tested end to end with real AI models.**
Each step is covered by automated tests that use simulated agents. A full run
from the first conversation to accepted work has not been completed with real
models yet. Try it on a small, unimportant change first.

**The window must stay open while work runs.**
MARSHAL has no background service. If you close the window, the work stops,
but its stored run remains recoverable. Reopen MARSHAL and use `/marshal
resume` to continue.

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

**The Marshal can read and write the whole project.**
Protection is at approval and merge. A task's file list does not restrict the
Marshal's project access.

**Applying a task needs stable project directories.**
MARSHAL cannot protect delivery of a task's changes if another process on the
same machine moves or replaces project directories at the same time. Do not
run other tools that rearrange the project while MARSHAL is applying a task.

**Native workers run with your user account's rights.**
They are trusted and use the agent's own permission controls. Governed workers
are sandboxed; a separate working copy alone does not provide that protection.

**Shared text can reach another provider.**
Sharing in the agents' shared channel sends one provider's text to another
provider when the receiving agent uses it. Local records do not make that
exchange local-only.

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

An agent's shared inbox excludes messages authored by that agent. Another
agent may quote or copy those messages in its own output, including tool
output. The original agent can then see its own words inside that peer's
message. MARSHAL does not remove nested quotes; author filtering applies to
the outer message, not every piece of text inside it. Channel history also
requires the relevant read grants before messages can be delivered.
