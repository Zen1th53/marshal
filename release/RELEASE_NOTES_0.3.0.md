# Marshal v0.3.0

This release comes from running Marshal on itself for a long stretch, as a real
owner would, and fixing what got in the way. The project now has one living
architecture that every goal changes visibly; the Board says what the Marshal
is doing and what it waits for; chat messages are delivered reliably; and
workers use your own provider sign-ins and say why they failed.

## Added

### One living project architecture

- **One architecture for the whole project.** Each goal is drawn as dashed
  changes on the current architecture: new parts and connections dashed and
  attached to existing parts, changed parts outlined with a before and after,
  removed parts faded and struck through. Existing parts stay solid.
- **Labelled connections and coverage.** Every connection says what flows
  along it, and every part says which requirements it delivers. The Marshal
  cannot submit an architecture that leaves a requirement uncovered.
- **"Only this goal's changes"** shows just the goal's changes, keeping the
  connected existing parts as context.
- **A History panel** lists each revision with its date, goal, run and who
  accepted it. After a run, parts show **built** or **differs from the
  architecture**.
- **Press `a` on the Board to open the architecture at any time**, between
  goals as well as when one waits for you.

### The Board shows the work

- **The goal the Marshal is working on** is on the Board home, with elapsed
  time, instead of "no run yet" while it plans.
- **The planning step is shown:** goal, requirements, drafting and reviewing
  the design, architecture, tasks.
- **It says what the Marshal chat waits for**, for example "open the chat (F11)
  and choose Yes" for an agy permission prompt, instead of "chat busy".
- **Startup says what it checks**, every provider verification leaves an
  Activity note, and `y` says why when there is nothing to open.
- **A reviewer command the Marshal asks about is put in plain words**
  ("The Marshal asks Codex to review its design draft (read-only)"); the raw
  command goes to Activity.

### Limits

- **Warnings when a provider window runs low.** One Activity note per window
  when a provider or agy quota group falls below 20%, and the providers row
  names the low window. At 0% with tasks queued, a note names who can take
  them.
- **A limit check before a plan runs**, once per plan, comparing the plan's
  task count with what is left.

## Changed

- **A switched Marshal continues your goal.** Changing the Marshal's provider
  mid-goal hands the active goal, requirements, design and architecture to the
  new Marshal instead of starting a new goal.
- **Workers use your own provider homes** (`CODEX_HOME`, `CLAUDE_CONFIG_DIR`);
  stale credential links in Marshal's governed homes are removed so another
  account is never reused.
- **Plans split per requirement.** The Marshal is asked for one task per
  independent requirement, and is warned when one task covers many.
- **Codex plans at high effort** unless you chose one.
- **Antigravity (agy) is quieter and safer.** Marshal's own files need no
  prompt and there is no automatic first turn; `ssh`, `scp`, remote `rsync`
  and `sudo` always ask you first.
- **The first Board frame no longer waits for the project index**, which builds
  in the background with a progress line.
- **Squash-merged branches are recognised**, and the architecture request
  waits until the requirements are confirmed.

## Fixed

- **A restart no longer strands a run.** If Marshal stops while a worker runs,
  the interrupted attempt is closed on the next start and the task runs again;
  the run used to stop with "its workspace didn't match the approved plan".

- **A stalled run can be continued from the Board.** When a run waits with a
  returned task and nothing working on it, the inbox offers retry and resume,
  and `r` on a stuck task retries it; `r` never types into the composer.
- **The Codex Marshal reaches its own tools.** Codex's sandbox refuses Unix
  sockets; Marshal now also takes its requests through a private folder the
  sandbox may write, without widening the sandbox.
- **An older control centre is found.** Starting Marshal while an older control
  centre runs on another tmux socket offers to replace it instead of opening a
  bare chat.

- **A Board message never joins your half-typed input** in the Marshal chat
  (a stray key used to arrive glued to a decision request). It waits, with a
  notice, and your text is never cleared.
- **A network drop no longer loses a message:** it is resent once, then paused
  with a clear note. Queued messages are kept while you or the network hold
  the chat up.
- **Long messages are sent**, and **choices work in every chat** (the hint asks
  for the choice word and Enter, as agy ignores an empty submit).
- **Answered proposals are not replayed** after a restart.
- **A first open killed halfway recovers** on the next start; `marshal doctor`
  names the condition and the fix if it cannot.
- **Sign-in failures are not quota.** "codex is not signed in for workers: run
  codex login (or set CODEX_HOME)", with no cooldown.
- **A live worker keeps its lease.** A slow but working worker no longer fails
  with "execution lease expired" after 5 minutes; the lease renews from the
  worker's heartbeats, and a silent worker is still caught by the stall
  watchdog.
- **Lease and tool-link failures are named** ("the task's time ran out",
  "Codex lost its connection to Marshal's tools") instead of looking like
  quota, and a pause after repeated failures says so.
- **Failures name their cause:** the architecture footer, a missing EGM (with
  the install command), a quota stop (with the retry time) and the worker's
  own error lines.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

Existing installs: press `F10` in the TUI or run `marshal update install`.
