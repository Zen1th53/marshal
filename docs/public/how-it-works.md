# How MARSHAL works

MARSHAL sits between you and your AI coding agents. Instead of typing a request
into one agent and hoping for the best, you go through five steps.

<div class="steps" markdown>

1.  **You describe the goal.**     You open a conversation with the Marshal and
say what you want, in your     own words.

2.  **The Marshal plans with you.**     It reads your project, asks questions
one at a time, and writes a plan:     what to build, split into small tasks,
each with a way to check it.     Each check names the acceptance criteria it
proves. A task cannot be     accepted while any criterion lacks passing
evidence.

3.  **You approve the plan.**     You read the plan and correct anything that
is wrong. Approval allows     MARSHAL to dispatch the planned work.

4.  **Agents do the work.**     Each task goes to an AI agent, which works in
its own separate copy of     your project. MARSHAL runs each task's checks
again when the agent says     it is done.     A task's scope can name files or
directories, including files inside them.

5.  **You review and accept.**     You look at the changes and accept them, or
send a task back with a note     saying what to fix.     MARSHAL integrates the
accepted version. Rejected results and merge failures     go back with reasons
and count toward the rework limit.

</div>

The Marshal can read and write the whole project. Protection is at approval and
merge: you approve the plan, and results are reviewed before they are
integrated. The Marshal is not restricted to reading files or to the files
listed in a task.

## Words you will see

Marshal :   The AI that plans with you and coordinates the work. You choose
which     model plays this role: Codex, Claude Code or Antigravity.

Agent (or worker) :   An AI coding tool that does one task of the plan. MARSHAL
works with     Codex, Claude Code, OpenCode and Antigravity.

Plan pack :   The written plan. It is a few plain text files: the requirements,
a list     of tasks, and one note per task. You can open and edit them.

Approval :   Your decision to let something go ahead, such as the plan or a
risky step     an agent wants to take. Agents cannot approve anything on your
behalf.

Checkpoint :   A saved snapshot of your project files. You can return to it
later.

Composer :   The input line at the bottom of the MARSHAL window. You type
commands     there. Commands start with `/`, for example `/help`.

## Two ways to use an agent

MARSHAL can open an agent in two ways, and it helps to know which one you are
using.

**Directly.** Commands such as `/codex` or `/claude` open the agent's own
interface, the same as running it yourself. These native workers are trusted
and run with your user account's rights, using the agent's own settings and
permission controls. MARSHAL does not sandbox them.

**From a plan.** Each task carries `mode: native` or `mode: governed`, shown
in the plan approval and `/marshal status`. Codex and Claude tasks default to
governed and require a credential grant and a current sandboxed capability
probe. If probe evidence is missing or stale, approval starts the probe before
dispatch. Antigravity and OpenCode plan workers use native mode. Ask for native
mode explicitly in the goal to use a trusted native worker.
A goal requesting governed work can produce governed Codex or Claude tasks.
Native workers remain trusted and run with your user account's rights. Governed workers are
sandboxed. A separate copy of the project keeps changes apart, but is not
itself a sandbox. Results are checked before they are accepted and merged.

## What stays on your computer

Your project, the plan and MARSHAL's records stay in your project folder.
Native sessions use your normal provider sign-in. Governed workers use the
[credential broker](credential-broker.md) only after your per-project
permission: API keys, Codex ChatGPT and Claude Code subscription tokens stay on
the host while the sandbox holds placeholders. The broker reads subscription
tokens afresh and never writes host sign-in files. Codex refresh uses its host
CLI protocol. Claude refresh happens during normal host CLI use; when its host
sign-in expires, run `claude` once on the host and retry the task. Do not paste passwords or API keys into the composer.

Sharing through the agents' shared channel sends one provider's text to another
provider when the receiving agent uses it in its conversation. The channel's
local files do not keep that text local once an agent uses it. Choose which
agents share with each other accordingly.

## Reviewing finished CLI tasks

A task completed with `marshal run TASK-ID` can enter the same review flow with
`/marshal import TASK-ID CHECK`, for example
`/marshal import TASK-hello grep -qx hi hello.txt`. Imported tasks have no
acceptance checks, so you supply one. Review the original task branch's changes
and `/marshal status`, then `/marshal approve` to check and review the pinned
result without rerunning its worker. In acceptance mode `user`, use
`/marshal accept TASK-ID` and `/marshal resume`; `/marshal close` delivers the
verified result to the default branch. Mode `marshal-then-user` asks for the
operator’s close approval after Marshal review; mode `marshal` uses the
standing close approval given with the plan. Plan approval binds the task revision,
base, result commit and check. Changes to that binding are refused. Normal
merge-driver, target-branch and close protections still apply.

Next: [Install MARSHAL](install.md).
