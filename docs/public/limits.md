# What MARSHAL cannot do yet

MARSHAL 0.0.7 is an early version. Knowing its limits helps you use it safely.

**It has not yet been tested end to end with real AI models.**
Each step is covered by automated tests that use simulated agents. A full run
from the first conversation to accepted work has not been completed with real
models yet. Try it on a small, unimportant change first.

**The window must stay open while work runs.**
MARSHAL has no background service. If you close the window, the work stops,
but its stored run remains recoverable. Reopen MARSHAL and use `/marshal
resume` to continue.

**tmux 3.3a or newer is required for the TUI.**
tmux 3.2a and older (including tmux 3.2a shipped with Ubuntu 22.04) are refused
because tmux 3.2a's server crashes during MARSHAL's popup and native-window use.
Use Ubuntu 24.04+, Debian 12+, or build tmux from source.

**It runs on Linux only.**
macOS is not supported yet. On Windows you can try WSL 2, which has not been
tested.

**Credential use requires your permission.**
Use `/permission credential request <codex|claude|gemini|opencode>` and press
uppercase `A` in MARSHAL's permission prompt. The decision lasts for this
project until `/permission credential revoke <provider>`. Every other key,
closing the prompt, and timeout deny. Model text cannot grant access.

The [credential broker](credential-broker.md) keeps supported API keys on the
host and gives governed workers unissued placeholders. Only the selected
provider's HTTPS host receives the real credential. Codex ChatGPT subscription
sign-in works through the broker using fresh host-file reads and a host CLI
managed-auth refresh; the worker never sees the token. MARSHAL never writes the
host sign-in file. Claude Code subscription sign-in also works through the
broker. If its host token expires or is rejected, the broker re-reads once and
retries only with a changed fresh token; otherwise it alerts and refuses the
request. Run `claude` once on the host to refresh it, then retry. See the broker details for supported CLI
formats, the 20-second refresh deadline, and buffering limits. Permission does
not override unsupported flows. Native sessions remain separate.

**OpenCode cannot be the Marshal.**
It can do tasks as a worker, but planning needs Codex, Claude Code or
Antigravity.

**Scope is checked after the worker runs.**
If an agent changes files outside its task or breaks an explicit constraint,
the task fails. Native workers can still write those files before the check
runs. Drift from the goal is only recorded as a warning; you see it when you
review.

**The Marshal can read and write the whole project.**
Protection is at approval and merge. A task's file list does not restrict the
Marshal's project access.

**Applying a task needs stable project directories.**
MARSHAL cannot protect delivery of a task's changes if another process on the
same machine moves or replaces project directories at the same time. Do not
run other tools that rearrange the project while MARSHAL is applying a task.

**Native workers run with your user account's rights.**
They are trusted and use the agent's own permission controls. Plan approval
shows each task's mode; Codex and Claude default to governed. A credential grant
does not change a native task into a governed task. Governed workers
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

**Finished CLI tasks need an acceptance check to enter Marshal review.**
Use `/marshal import TASK-ID CHECK` for a successful Codex or Claude task in
review. It creates a plan awaiting approval, pins the existing result and runs
sandboxed checks and normal review before merging. `/apply` still takes only
Codex cloud task IDs. Import does not invent evidence for the task's original
intent; choose a check that proves the result you want. Delivery can refuse if
the default branch has diverged or is checked out; follow the close command's
instructions and retry.
