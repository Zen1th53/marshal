# Command reference

Type these in the MARSHAL composer. Words in capitals, such as `TASK-ID`, are
placeholders: replace them with a value MARSHAL shows you. `/help` lists the
commands available in your version.

## Basics

| Command | What it does |
| --- | --- |
| `/help` | Show commands and keys. `/?` does the same. |
| `/status` | Show the state of this session. |
| `/diff` | Show what changed in your project. Add `staged`, `unstaged` or `untracked` to see only those changes. |
| `/doctor` | Check that everything is set up. |
| `/update` | Check for a new version. `/update install` installs it. |
| `/quit` | Leave MARSHAL. `/exit` does the same. |

## Planning with the Marshal

| Command | What it does |
| --- | --- |
| `/marshal` | Show the Marshal's status. |
| `/marshal chat` | Start a planning conversation. |
| `/marshal model codex` | Choose the AI that plans: `codex`, `claude` or `agy`. |
| `/marshal settings` | Show or change [settings](settings.md). |
| `/marshal approve` | Approve the plan and start the work. |
| `/marshal status` | Show the plan's progress. |
| `/marshal approve-task APPROVAL-ID` | Allow a step an agent asked permission for. |
| `/marshal accept TASK-ID` | Accept a finished task. |
| `/marshal return TASK-ID NOTE` | Send a task back with a note on what to fix. |
| `/marshal resume` | Continue after a decision, a stop or a restart. |
| `/marshal stop` | Stop the run and keep its state. |
| `/marshal amend REASON` | Ask to change the plan. Big changes need `/marshal amend approve` or `/marshal amend deny`. |
| `/marshal close` | Finish a completed run and bring the work into your project. |

## Goals and approvals

| Command | What it does |
| --- | --- |
| `/goal` | Show your current goal. |
| `/goal create TEXT` | Create a goal. |
| `/goal edit TEXT` | Change the goal. Needs your confirmation. |
| `/goal add-constraint TEXT` | Add a rule the work must follow. |
| `/goal constraints` | List the rules. |
| `/goal progress` | Show which success criteria are met so far. |
| `/approvals` | List decisions waiting for you. |
| `/approve ID` | Approve one of them. |
| `/reject ID REASON` | Reject one of them. |

## Running work

| Command | What it does |
| --- | --- |
| `/pause`, `/resume`, `/cancel` | Pause, continue or stop a run. Add `run:RUN-ID` to choose one. |
| `/budget` | Show limits and how much has been used. |
| `/budget set calls=20 duration=30m` | Set limits. Needs your confirmation. |
| `/budget clear` | Remove the limits. Needs your confirmation. |
| `/tasks` | List tasks in this project. |
| `/agents` | List the agents taking part. |
| `/msg all TEXT` | Send a message to the team of agents. |

## Agents

| Command | What it does |
| --- | --- |
| `/codex`, `/claude`, `/opencode`, `/agy` | Open the agent's own interface. |
| `/codex resume --last` | Continue the latest Codex conversation in this project. |
| `/claude continue` | Continue the latest Claude conversation. |
| `/codex exec TEXT` | Ask Codex to do a task under MARSHAL's control. `/claude exec` works the same way. |
| `/sessions` | List past conversations and runs. |
| `/provider status` | Show which agents MARSHAL found. |
| `/models` | List Codex models. |
| `/model select codex MODEL` | Choose the model for future work. |
| `/effort high` | Choose how hard Codex thinks. |
| `/apply CODEX-TASK-ID` | Bring a Codex cloud task's changes into your project. |
| `/codex cli ARGUMENTS` | Pass options straight to the agent. Every agent supports `cli`. |

## Undo and backups

| Command | What it does |
| --- | --- |
| `/checkpoint create REASON` | Save a snapshot of your project files. |
| `/checkpoint list` | List snapshots. |
| `/checkpoint diff OLD-ID NEW-ID` | Compare two snapshots. |
| `/rollback CHECKPOINT-ID` | Preview going back to a snapshot. It gives you a confirmation command to run. |
| `/backup create` | Back up MARSHAL's records. |
| `/backup restore PATH` | Preview restoring a backup. It gives you a confirmation command to run. |

## Memory

| Command | What it does |
| --- | --- |
| `/memory search TEXT` | Search what was said in past conversations. |
| `/memory list` | List what MARSHAL remembers. |

## ULTRA

| Command | What it does |
| --- | --- |
| `/ultra status` | Show whether you have ULTRA and whether it is on. |
| `/ultra start` | Switch ULTRA on for this session. |
| `/ultra stop` | Switch ULTRA off; confirm with `/ultra stop confirm`. |
| `/ultra request` | Ask for access. |

??? info "Advanced commands"
    These show MARSHAL's internal records. You do not need them for everyday
    work.

    | Command | What it does |
    | --- | --- |
    | `/checkpoint inspect ID` | Check a snapshot and show its contents. |
    | `/approval inspect ID` | Show the details of an approval. |
    | `/evidence list` | List evidence that agents produced. |
    | `/claims` | List the claims made about the goal. |
    | `/alignment` | Show warnings about agents working outside their task. |
    | `/fingerprint` | Group failures that keep happening. |
    | `/policy` | Show the safety rules in force. |
    | `/sandbox` | Show how agents are isolated. |
    | `/runtime` | Show MARSHAL's stored runtime state. |
    | `/store check quick` | Check MARSHAL's database. |
    | `/skills` | List agent skills and where they come from. |
    | `/export` | Save a private bundle of evidence for the goal. |
    | `/route` | Show which agent MARSHAL would suggest for a task. |
    | `/mcp`, `/plugin`, `/features` | Manage Codex add-ons. |
