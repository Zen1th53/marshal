# How MARSHAL works

MARSHAL sits between you and your AI coding agents. Instead of typing a request
into one agent and hoping for the best, you go through five steps.

<div class="steps" markdown>

1.  **You describe the goal.**
    You open a conversation with the Marshal and say what you want, in your
    own words.

2.  **The Marshal plans with you.**
    It reads your project, asks questions one at a time, and writes a plan:
    what to build, split into small tasks, each with a way to check it.
    Each check names the acceptance criteria it proves. A task cannot be
    accepted while any criterion lacks passing evidence.

3.  **You approve the plan.**
    You read the plan and correct anything that is wrong. Nothing happens to
    your code until you approve.

4.  **Agents do the work.**
    Each task goes to an AI agent, which works in its own separate copy of
    your project. MARSHAL runs each task's checks again when the agent says
    it is done.
    A task's scope can name files or directories, including files inside them.

5.  **You review and accept.**
    You look at the changes and accept them, or send a task back with a note
    saying what to fix.
    MARSHAL integrates the accepted version. Rejected results and merge failures
    go back with reasons and count toward the rework limit.

</div>

## Words you will see

Marshal
:   The AI that plans with you and coordinates the work. You choose which
    model plays this role: Codex, Claude Code or Antigravity.

Agent (or worker)
:   An AI coding tool that does one task of the plan. MARSHAL works with
    Codex, Claude Code, OpenCode and Antigravity.

Plan pack
:   The written plan. It is a few plain text files: the requirements, a list
    of tasks, and one note per task. You can open and edit them.

Approval
:   Your decision to let something go ahead, such as the plan or a risky step
    an agent wants to take. Agents cannot approve anything on your behalf.

Checkpoint
:   A saved snapshot of your project files. You can return to it later.

Composer
:   The input line at the bottom of the MARSHAL window. You type commands
    there. Commands start with `/`, for example `/help`.

## Two ways to use an agent

MARSHAL can open an agent in two ways, and it helps to know which one you are
using.

**Directly.** Commands such as `/codex` or `/claude` open the agent's own
interface, the same as running it yourself. It uses its own settings and
permissions. MARSHAL only remembers the conversation.

**Through MARSHAL.** When the agent works on a task from an approved plan,
MARSHAL controls it: the agent works in a separate copy of the project, asks
you before risky steps, and its result is checked before you accept it.

## What stays on your computer

Your project, the plan and MARSHAL's records stay in your project folder.
The agents use their own accounts and sign-in, the same as when you use them
on their own. Do not paste passwords or API keys into the composer.

Next: [Install MARSHAL](install.md).
