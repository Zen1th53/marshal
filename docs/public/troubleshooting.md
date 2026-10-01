# Troubleshooting and FAQ

## MARSHAL cannot open the project

Enter a Git project, run `marshal init`, then `marshal doctor`. Check directory
permissions. In an attached TUI, `/store check quick` verifies the state database;
`/store check full` performs a fuller integrity check with a five-second timeout.
A timeout is not a successful check.

## The provider is unavailable or authentication is unknown

Check its binary is installed and discoverable, then run
`marshal doctor --probe-providers`. Sign in through the provider's own interface.
An availability probe is not an authenticated execution test. Other CLI versions
can show unqualified operations; see [provider setup](providers.md).

## A command does nothing

Use `/help`. Plain composer text does not launch work. Bare `/marshal` shows
status; `/marshal chat` starts the conversation. Subcommand typos are refused.
When completion is selected, Enter accepts that choice first; submit the
finished command afterwards. Native operations need an interactive terminal.

## Why is execution blocked?

Read the reported policy or approval reason. Governed providers that need network
access can be blocked when policy cannot enforce it. Do not treat installed
binaries as proof that a run can execute. `/approvals` lists pending decisions;
inspect them before approval. Goal changes invalidate stale bindings.

## Why does the budget show UNKNOWN?

Some providers do not report cost or tokens. Enforced limits use model calls and
duration, checked before the next task. See [budgets](run-control.md).

## Why cannot I open navigation?

Navigation is closed in this build. Use composer commands, including with ULTRA.

## How do I recover from a bad apply or restore?

Read the checkpoint reported by `/apply` or rollback. Preview with `/rollback
<id>` before confirming. For state restore, stop other windows and the daemon;
use the [recovery guide](recovery.md). A file rollback does not restore the database.

## Is imported conversation memory verified?

No. It is candidate information. Search it with `/memory search <query>` and
verify important claims against project files and evidence.

## Can I close the window during a Marshal run?

Keep it open. After an interruption, inspect `/marshal status` and use
`/marshal resume` when the run can continue. Do not assume a stored run is still
executing.

## Does updating change the current process?

No. `/update install` verifies the downloaded archive checksum and installs the
latest release; restart to use it. Select candidates deliberately as described
in [installation](installation.md).
