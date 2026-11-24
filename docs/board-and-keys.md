# The Board and Keyboard Shortcuts

The Board is Marshal's control centre. In the terminal it is one short **home screen**: what is happening, the one thing needed from you, and where the **Web Board** is. The Web Board, in your browser, is the full interface: the plan, lanes, tasks, workers, activity, the Marshal's steps and requests, the readiness checklist, and every card with its buttons.

`marshal` opens it in a tmux workspace. The Marshal chat (the provider CLI the Marshal runs in) docks on the left when you open it; the Board stays on the right. The Web Board starts with the Board and opens in your browser when the computer has a display.

---

## The home screen

```text
 MARSHAL · demo · PAUSED · 3 workers · 14:05

 Now:       paused · 3 running · 9 queued (4/12 done)
 From you:  run paused: rv06 stuck — y move rv06 to codex and continue
 Web:       opened in your browser (w opens it again)
 Work:      2 active / cap 4 · 1 waiting
 Lanes:     claude-1 T03 4m · agy-1 review T02 9m STALE
 Tasks:     T02 review · T03 building · T04 waiting (dependency) · +2 more
 Marshal:   claude Opus 5.5 high · context 42% · 5h 61% · weekly 83%
 Activity:  14:04 · review · T02 sent for review ×2
 ⏎ chat   w web   s say   r run   i inbox   c checks   ? keys   q quit
```

- **Top line**: the project, the state (`READY`, `PLANNING`, `YOUR TURN`, `RUNNING`, `PAUSED` …), the running workers and the clock.
- **Now**: one sentence. During a run: the tasks running, queued and done; before one: what the Marshal is doing.
- **From you**: `nothing` (with the next thing you will be asked), or the one thing that needs you:
  - a safe step, named with its key `y`: retry a stuck task, move it and continue, resume, wait, confirm the requirements (`e` then a line's letter edits one line). `y` does what the card's main button does, once: a second `y` within two seconds does nothing more;
  - a decision with authority (allow or deny a request, a gate, the plan, the architecture, changed files, a change of goal, the Marshal's question card, stopping the run): `open the web to decide (w)`;
  - the Marshal asked you something in its chat and waits: `⏎ open chat`;
  - anonymous usage counts on first use: `y` keeps them on, `t` turns them off (docs/telemetry.md lists every field);
  - agy not signed in: `g` opens its sign-in window.
- **Web**: `opened in your browser`, `open with w`, or, on a computer with no display, `no display here: /marshal web shows the link to open`. The Web Board's private link is never on the home screen.
- **Host**: only when the host, an update or MCP needs attention: the host notice, the thermal guard and its lane cap, low disk, `update <tag> (F10)` and `MCP failed (F6)`, with `2` for the Host view. It has its own row, so a command answer on the last row does not hide it.
- **Work**, **Lanes**, **Tasks**, **Marshal**, **Activity**: active work against the cap, each lane's task and time (`STALE` when its worker went quiet), the tasks not merged yet with why they wait, the Marshal's provider, model, effort, context and quota left, and the latest Activity line. A line that does not fit ends with `+N more`.

The home screen draws at any size down to about 40 × 10: the blank row goes first, then the lower lines.

### Run, Inbox and Readiness

Three pages show the rest of what the Web Board shows, in the terminal; `Esc` or `1` returns to the home and every home key still works on them. `j`/`k` or the arrows move the selection, which stays on the same lane or task while the list changes or the window is resized; `PgUp`/`PgDn` move a page.

- `r` **Run**: the stages, progress, ETA, merges per hour, rework, token total and throughput; every lane with its provider, model, task, step, elapsed time, last signal and why it waits; every task with its writer, reviewer and why it waits (code, blocker, until when); Activity; the Marshal, its steps and requests with their answers; and the chat's health (bound, answers, restarts, rebinds, resends). At 120 columns or more Activity and the Marshal sit beside the work.
- `i` **Inbox**: the From-you line, the decision and paused cards, every queued item, each approval with its age, expiry and actions, and what each waiting task waits for. Decisions are made on the Web Board (`w`).
- `c` **Readiness**: the readiness checks with their hints, the defaults, the provider caps and limits (percent left, window, reset) and the host.

---

## Keys

| Key | Action |
|---|---|
| `Enter` | Open the Marshal chat. Always: `Enter` never decides anything on the Board. |
| `F11` | Switch between the chat and the Board. In tmux, `Alt+←` moves to the chat and `Alt+→` to the Board. |
| `y` | Do the safe step **From you** names. |
| `w` | Open the Web Board in your browser. With no display, the answer shows its link (as `/marshal web` does). |
| `s` | Say something to the Marshal: opens the composer; type and press `Enter`. |
| `a` | Open the current architecture in your browser (also `/marshal architecture`). |
| `m` | Choose the Marshal's provider and model. |
| `e` | With the requirements waiting: edit a line (then its letter). |
| `r`, `i`, `c` | Open the Run, Inbox or Readiness page. |
| `Space` or `Ctrl+K` | Open the action palette. |
| `/` | Type a command, such as `/marshal settings` or `/task-model T03` (the model a task runs on). Every setting, its default in each edition and when it applies is in the [settings reference](configuration.md#settings-reference). |
| `?` | Every key. |
| `q` | Quit. |
| `Ctrl+X` | Stop all workers (never the Marshal). |
| `Ctrl+C` | Interrupt; press it twice to leave. |

A letter that is not a Board key starts a message to the Marshal (`to the Marshal › …`); `Esc` cancels it.

The first-run **Marshal picker** owns the keyboard until you answer it, even when text was left in the composer.

### The action palette

`Space` opens the palette with the eight core actions first: say to the Marshal (`s`), open the architecture (`a`), retry or continue (`y`), change model or provider (`m`), pause everything (`p`, the same as `/pause all`; `/resume all` continues), stop all workers (`Ctrl+X`), limits and workers (`l`, the Providers view) and help (`?`). Type a word to search the rest. An action that cannot run now shows why. The inbox's allow and deny are on the Web Board, not in the palette.

### Views

The views open over the home screen and keep their own keys; `Esc` or `1` returns.

| Key | View |
|---|---|
| `2` or `h` | Host |
| `3` | Memory |
| `4` | Providers (`v` checks the next provider; `Enter` runs the check) |
| `5` | Verify (after a run starts) |
| `6` | Security (after a run starts) |
| `7` | Plan (after a run starts) |
| `8` or `b` | Architecture |

### Function keys

`F1` help, `F2` review, `F3` diff, `F4` status, `F5` models, `F7` native Codex, `F8` native Claude, `F9` native OpenCode, `F10` update, `F12` native Antigravity. `F6` appears only after an MCP failure and shows what failed.

### Marshal chat

The docked chat's pane title is the **MARSHAL CHAT** strip: the provider and model, whether the chat is bound to this run (`● live`), the stage, how many requests it answered and the input state (`input clear` or `input waiting`; nothing before the Board has looked at it), then `[m] switch`. Everything below the strip is drawn by the provider CLI itself.

---

## The Board in your browser (Web Board)

The Web Board is the Board as a simple web page. It updates live (at most four times a second) and works at phone width, so you can follow and steer a run from a phone through your own remote desktop to this computer.

- It starts with the Board and opens in your browser where there is a display (`MARSHAL_WEB_OPEN=0` keeps the browser closed). `w`, the palette entry **Open the Board in the browser** or `/marshal web` open it again, each time on a fresh one-use link. Where there is a display the footer names only the address, never the link. On a computer with no display (`/marshal web`, or `w`), or when `/marshal web` cannot open the browser, the footer shows the link, and you open it in a browser on that computer.
- It runs inside the open Board and stops when the Board closes. It listens on `127.0.0.1` only, at a random port, and never on the network.
- The link carries a private, random secret (`http://127.0.0.1:PORT/?t=…`) that works once. The page takes it out of the address bar and the history at once and trades it for the page's own secret, which it keeps for that browser tab only (a reload still works) and sends with each request. No cookie is used. A copy of the link left anywhere else (the footer, the browser's history, the command that opened the browser) is worthless once your page has opened; run `/marshal web` again for a new link, for example for a second tab. If a link was already used by something else when your page opens it, the Web Board shuts every page out and says so: open a new link. Without a link the page shows no data and can do nothing; a link from an earlier Board no longer works. **Do not share the link: anyone on this computer who has it can act for you on the Board.** It never goes to Activity, the Marshal chat or any file the Marshal reads.

### What the page shows

- At the top, one sentence of what is happening now and one **From you** line.
- With no card, during a run: **Retry <task>** when the Board's `r` would retry a stuck task, and **Stop all workers**.
- When the Board waits for you, the card: its title, the cause in one line, what the main button will do, one big button with the card's main action (for example **Retry T03 in a fresh session**, **Move to claude**, **Continue the run**) and at most two text buttons (for example **Wait until 21:10**, **Stop the run**).
- One box, **Tell the Marshal…**, with **Send**.
- Everything else (the card in full, Plan, Lanes, Tasks, Workers, the Marshal, Inbox, Activity, Marshal steps, Requests, readiness) under **Details** sections: closed on a phone, open on a wide screen.

### What you can do from the web

Only what the Board offers right now. Each button presses the card's key through the Board's own handler for it; nothing on the page builds a command of its own.

| On the page | The same as in the terminal |
|---|---|
| The paused card's big button | `Enter` on the card: it runs exactly what the card says, for example `/marshal retry T03 fresh-session` then `/marshal resume`, `/marshal reassign T06 claude` then `/marshal resume`, `/marshal resume`, or waits for the limit reset |
| **Wait until …** | `w` on the paused card |
| **Raw error** (omitted when the card has more than three actions) | `d`: the answer shows on the page |
| **Stop the run** (two clicks: the first only asks) | `x` on the paused card (`/marshal stop`; the run and its work are kept) |
| A decision card's buttons | its keys: the plan's reply and the inbox's allow or deny ask for a second click (**Confirm …**), as `Enter` does after the first key in the terminal; a hard gate asks you to type its id |
| **Open in browser** on the architecture card | `a`: the review opens on this computer, and the page shows the review link. The architecture is accepted only in its own review page, never from the Board |
| **Comment** | moves you to the **Tell the Marshal…** box |
| **Retry T03** with no card shown (two clicks), offered only while a task is stuck | `y` on the home screen: the task starts again in a fresh session and the run continues |
| **Stop all workers** with no card shown, during a run (two clicks) | `Ctrl+X` (`/stop all`): every worker stops, never the Marshal |
| **Tell the Marshal…** + **Send** (at most 2000 characters) | `s` on the Board: the message goes to the Marshal chat as yours, never as a command, even when it starts with `/` |
| Every other card button (allow, deny, gates, the plan's reply, changed files, a change of goal, the question card, "what next?") | the card's own key; the terminal home only says `open the web to decide (w)` |

Not offered on the page: quitting, copying a link to the terminal's clipboard, opening a terminal view, and editing one requirements line (`e` on the terminal home; the page confirms them all).

After each click the page says what happened: **Done** (with the Board's own answer; when the button runs commands, such as a retry and a resume, the page waits for them, up to 15 seconds, and says **Started** if they are still running), **Sent** or **Queued**, or **Refused** with the plain reason (a command that failed is refused with its error) (for example "The Board changed since this page showed it; look again", or "No Marshal chat is open"). Every action writes the Activity line the terminal key writes, plus "From the web: …".

A button works once and only for the card the page shows: if the card changed, or the same click arrives twice, it is refused and the page shows the Board as it is now. Nothing is loaded from the internet, and the page never shows prompts, the chat transcript, file contents or secrets.

---

## Narrow terminals and ASCII mode

- **Size**: the home screen draws at any size; below about 40 × 10 its lower lines go. Beside the chat the Board gets half the window, and at least 60 columns while the chat can keep 40. The views need 80 columns on their own (60 beside the chat) and 12 rows; smaller, a view says the Board needs more space.
- **Colour**: truecolor when the terminal says so (`COLORTERM=truecolor`), 256 colours otherwise. `marshal tui --theme monochrome` draws the home screen in monochrome; `--theme high-contrast` styles overlays such as the palette, diff viewer and composer, while the home screen uses its usual colours; `NO_COLOR` or `marshal tui --no-color` turns colour off.
- **ASCII**: a locale that is not UTF-8 (for example `LC_ALL=C`), or `--no-color`, draws the home screen in ASCII:

```text
 MARSHAL . demo . RUNNING . 3 workers . 14:05

 Now:       3 running . 9 queued (4/12 done)
 From you:  nothing
 Web:       opened in your browser (w opens it again)
 Enter chat   w web   s say to the Marshal   q quit
```

---

## Architecture lifecycle

The project has one living architecture. For its baseline, the Marshal describes the whole project, with all parts marked new. Each later goal proposes the next revision: the Marshal keeps existing parts, describes what each connection carries, and ties the requirements to components.

Press **`a`** on the Board, or use **`/marshal architecture`**, to open the current architecture at any time. On the Web Board's Accept architecture card, **Open in browser** opens the proposal waiting for your acceptance. The browser header shows `rev N → N+1 · proposed` while waiting and `rev N · current` after acceptance.

Thin grey borders and edges show current parts. Bold blue solid borders and edges show additions, with a “new” tag on parts. Changed parts have a solid amber outline and a “changed” tag; changed connections have an amber highlight. Open a part or connection to see its before and after fields. Removed parts are faded and struck through. Use **Only this goal’s changes** to focus on the proposal while keeping connected existing parts as context.

Comment on the drawing, choose builders if available, and select **Accept architecture** when ready. The Marshal then plans the work and continues the run. **History** lists revisions with their date, goal, run, and who accepted them. Select a revision to inspect its drawing and changes against the preceding revision; return to the latest architecture to comment or accept.

After a run, components whose paths were changed can show **built**. A failed conformance check marks them **differs from the architecture**, so you can see where the implementation needs attention. These marks appear when the run supplies conformance results.
