# Marshal v0.2.12

This release makes what you see match what Marshal really does: every action
in the palette is one you can take, the vitals row runs across the whole
terminal, and the Marshal's words, the Board and the commands say things
plainly.

## Fixed

### The palette

- **The palette lists only your actions.** Space or Ctrl+K now shows the
  Board's own keys and the few commands no key runs (pause all, resume all,
  doctor, quit). The internal menus (claims, evidence, budget, goal, team,
  policy and the like) are gone from it.
- **One key, one action.** Chords that did nothing were removed, and no two
  actions share a key.

### Vitals and the Marshal row

- **One vitals row across the whole terminal.** In the tmux session Marshal
  creates, the vitals row sits above every pane and window: the Board, the
  docked or full-screen chat and the workers. While the chat is docked, a
  second row carries both panes' titles. Your own status line comes back
  when you quit; a tmux session you made yourself keeps its own status line.
- **The vitals row is on every screen**, including the launch picker, the
  opening and the Blocked screen, and full width above both halves of the
  chat split.
- **The chat's top row shows the Marshal model, its live limit and
  `[m] switch`.** Click `[m]` to open the Marshal picker on the Board. At
  narrow widths the row shortens in a fixed order, but the percent left and
  `[m]` always stay. Typing `m` in the chat still goes to the provider.
- **Vitals give way in a fixed order on narrow terminals**, and every limit
  Marshal cannot read says the same thing: "limit unknown" ("?" when narrow).
- **Normal vitals text is readable.** RAM and healthy limits were painted
  black; they now use your terminal's own colour.
- **The low-disk warning follows one rule everywhere:** less than 10 GB free
  or less than 5% of the disk, whichever is smaller.
- **No "Marshal — approval authority" subtitle** in the status line, the
  splash or the startup output.

### The Board

- **MCP shows only when it fails.** Help lists F6 only after an MCP failure,
  and F6 then says plainly what failed; while MCP works, F6 just says so.
- **Counts read correctly**: "1 task", "2 tasks", and so on.
- **The Board says "planning" while the Marshal plans the tasks**, and still
  says so after a restart in the middle of planning.
- **Views 5, 6 and 7 (Verify, Security, Plan) are off until there is a run.**
  Pressing their key before then says why in one plain line.
- **Sent-back tasks have their own column**, and the `m` picker uses plain
  words.
- **`w` (task model) is in the hint bar**, and the hint bar always keeps `?`
  for help, however narrow the terminal.
- **Activity shows the Marshal's decisions in plain words.**
- **The Providers view lists the providers you have installed.**

### The Marshal chat

- **The Marshal opens with "Hello."** (or "Hello again." when it resumes)
  instead of "Start." or "Continue.".
- **Notes Marshal sends to the chat start with `[Marshal]`**, so they are
  never taken for your words. The run-closed note reads, for example,
  "[Marshal] The run closed: 3 of 4 tasks verified and merged to main."
- **The Marshal's own tool calls start with a plain line** (for example
  "Proposal received; it waits for the person's decision.") instead of raw
  data.
- **Codex usage tips are off** in the Marshal chat.
- **The Marshal tells you the controls you really have**: Ctrl+X stops all
  workers (never the Marshal), pause and resume are in the action palette,
  and the architecture opens on `y`. It no longer tells you to press A in a
  popup that does not exist in this build.

### Setup and commands

- **Opening Marshal in an existing repository sets the project up** instead
  of asking you to run setup first.
- **`marshal doctor` and `marshal status` speak plainly**: the project and
  its branch, the run and how many tasks are done, the Marshal provider, and
  "Marshal isn't set up in this project yet" when it is not.
- **`/egress`, `/provider`, `/mcp` and `/optimization` answer in plain
  words** (for example "No network calls recorded yet.", and which providers
  are installed or missing).
- **Sign-in permission messages say what happens** ("Asked to let tasks use
  codex's sign-in in this project. The Marshal decides.").
- **`marshal verify` names the provider** it checked.
- **A task that could not start is tried once more** ("couldn't start yet;
  Marshal is trying it once more") before it counts as returned.
- **`/status` and the help footer show whether anonymous telemetry is on or
  off**, once.
- The splash says the same in every edition.

### Pause and resume

- **Pause and resume work from the palette.** "Pause all" pauses every
  running task of the session and the Marshal starts no new task until you
  resume; "Resume all" starts them again and lets the Marshal go on. Each row
  is on only when it can act and otherwise says why.
- **Pausing no longer hangs the Board.** A deadlock between pausing and the
  running engine is fixed.
- **Pausing the run while tasks are working no longer stops or breaks it:** a
  task that had not started waits, a task that was working finishes its step,
  and after you resume, the Marshal carries on and the run completes.
- **When something cannot be paused or resumed, Marshal names the task or
  says "one part of the run"** instead of showing an internal id.

## Changed

- The shipped user docs no longer say that you approve the plan or review
  commands before they start: the Marshal approves the plan itself. The one
  gate that stays is your acceptance of the architecture.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

Existing installs: press `F10` in the TUI or run `marshal update install`.
