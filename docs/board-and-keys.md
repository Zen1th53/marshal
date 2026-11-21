# The Board and Keyboard Shortcuts

The Board is Marshal's control centre. From Marshal 0.3.5 it is drawn as **Board v2**: titled panes on one grid, a top bar, a NOW line that always says what waits for you, and cards for the moments when the run needs your decision.

`marshal` opens it in a tmux workspace. The Marshal chat (the provider CLI the Marshal runs in) docks on the left when you open it; the Board stays on the right.

The classic Board is still available for one release: see [Classic Board for one release](#classic-board-for-one-release).

---

## The top of every screen

```text
  MARSHAL v0.3.5   project marshal-work / run review-fixes   RUNNING     ✳ claude · opus-5.5 · high  m   8 workers  inbox 0  19:31
 NOW  marshal claude Opus · watching reviews    │ work 8 running · 4 queued           from you nothing · next ask: final review
```

- **Top bar**: the version, the project and run, the state word, the **Marshal segment** (provider glyph, model and effort, followed by `m` to change it), the number of live workers, how many requests wait in the inbox, and the time.
- **State words**: `READY` (no run yet), `PLANNING`, `YOUR TURN` (something waits for you), `STARTING`, `RUNNING`, `REVIEWING`, `FINAL CHECK`, `MERGING`, `DONE`, `PAUSED` or `2 PAUSED` (how many tasks a limit stopped), `NOT RUNNING`.
- **NOW line**: what the Marshal is doing, what the workers are doing, and **from you**. *from you* is never empty: it names the first thing that waits for you (and `+N more`), or says `nothing` together with the next thing you will be asked, such as `next ask: architecture review` or `next ask: final review before merge`.

The vitals row (provider limits, CPU, RAM, disk, running workers) stays above the top bar on every screen. The footer and `/status` say whether anonymous telemetry is on or off; `marshal telemetry disable` turns it off.

---

## Screens

The Board picks the screen from the state of the project: a card that needs you first, then the run, then planning, then readiness.

### Readiness (no run yet)

The first open (after you choose **Who plans?**) shows what is ready:

```text
╭─ READINESS · 5 of 6 ready ────────────────────────────────────────────────────────╮ ╭─ DEFAULTS · this project ──────────────────╮
│                                                                                   │ │ workers   claude 2 · agy 4 · codex 4       │
│  ✓  Marshal    codex · GPT-6.2-Sol · signed in                           in use   │ │ fallback  codex → claude → agy             │
│  ✓  Workers    claude · up to 2 at once                                   ready   │ │ review    other provider                   │
│  !  Workers    agy is installed but not signed in                        action   │ │ approval  you: architecture · tasks: Marsha│
│                run agy once in a terminal and sign in                             │ │                                            │
│  ✓  Platform   Linux amd64 · workers can run here                         ready   │ │                                            │
│  ✓  Repo       clean · main @ 16091d7                                     ready   │ │                                            │
│  ✓  Browser    no display: links are shown on the Board                   ready   │ │                                            │
```

- **READINESS** lists the Marshal, each installed worker provider (signed in, not signed in, not checked yet, at its limit, or low on its weekly limit), the platform, the repository and the browser. A row that needs you says how to fix it on the line below.
- **USAGE COUNTS** appears beside it on a first open while anonymous telemetry is on and you have not been told about it yet. It says what is sent ([Telemetry](telemetry.md) has the details): `y` keeps the counts on, `t` turns them off (the same as `marshal telemetry disable`), `?` shows what is sent. The card goes away after `y` or `t`.
- **DEFAULTS · this project** shows how many workers each provider may run, the failover order, who reviews, and who approves what.

Keys here: `s` say what to build, `⏎` open the Marshal chat, `v` check the next provider that has not been checked (one model call; `Enter` runs it, any other key cancels), `m` choose the Marshal, `Space` actions, `?` help. Clicking a provider row arms its check. When agy is installed but not signed in, `g` opens it in a new tmux window in the project so you can sign in; when you close that window the Board arms its check, and `Enter` runs it.

### Planning

After you say the goal, the Board shows the Marshal's work before a run exists:

```text
╭─ PLAN ────────────────────────────────────────────────────────────────────────────╮ ╭─ REQUESTS TO THE MARSHAL ──────────────────╮
│ ✓ goal → ✓ reqs → ✓ design → ✓ arch rev 1 →  tasks · 6m  → build → review → merge │ │ SENT   REQUEST                   ANSWER    │
╰───────────────────────────────────────────────────────────────────────────────────╯ │ 18:44  apply architecture      allow 3s    │
╭─ MARSHAL STEPS · from its tool calls ────────────────────────────────── quiet 0m ─╮ │ 18:42  parallel limit 8        allow 5s    │
│ 18:47  draft    12 tasks · review.html edits ordered in 3 waves                   │ ╰────────────────────────────────────────────╯
│ 18:46  assign   4 claude sonnet · 4 agy gemini-3.8 · review on other              │ ╭─ CHAT · Marshal ───────────────────────────╮
│ 18:43  read     requirements.md · design.md · 18-row regression matrix            │ │ binding   this run · live                  │
│ 18:41  start    planning on claude Opus 5.5 (plan model)                          │ │ restarts  1 · rebound 18:41                │
```

- **PLAN**: the stages from goal to merge, with the current one highlighted.
- **MARSHAL STEPS**: what the Marshal did, read from its tool calls, and how long it has been quiet.
- **REQUESTS TO THE MARSHAL**: what the Board sent the Marshal and its answer (allow, decline) with the time it took.
- **CHAT · Marshal**: whether the chat is bound to this run, how often it restarted and was rebound, how many open requests were sent again, whether its input box is clear, its model and how much context it has used.

The Marshal steps pane lists the Marshal's recent tool calls, one short line each (read the context, checked the architecture, recalled memory, ...), and how long it has been quiet. To run the Marshal on another model, press `m`.

### Running

```text
╭─ PLAN · 12 tasks · 18 requirements ────────────────────── accepted rev 1 · 18:52 ─╮ ╭─ MARSHAL ─────────────────────── m switch ─╮
│ ✓ goal → ✓ reqs → ✓ design → ✓ arch → ✓ tasks →  build 5/12  → review → merge     │ │ ✳ claude  opus-5.5 · effort high    in use │
│ ███████████████████████████████░░░░░░░░░░░░░░   done 5 · active 6 · eta 45m       │ │ context ████░░░░░░  41% · 82k of 200k      │
╰───────────────────────────────────────────────────────────────────────────────────╯ │ chat    bound to this run · input clear    │
╭─ LANES · from worker heartbeats ────────────── limit claude 4 · agy 4 · total 12 ─╮ ╰────────────────────────────────────────────╯
│    LANE        MODEL        TASK                          STEP        TIME SIGNAL │ ╭─ WORKERS · providers ──────────────────────╮
│  ⣾ claude-1    sonnet-5.5   T03 sub-view external links   tests        14m     8s │ │ ✳ claude  ██████░░░░   4 lanes  sonnet     │
│  ◎ claude-4    sonnet-5.5   review T02 (by agy-1)         reviewing     2m     3s │ │ ⬡ codex   █░░░░░░░░░   spare    wk 11%     │
╰───────────────────────────────────────────────────────────────────────────────────╯ ╰────────────────────────────────────────────╯
╭─ TASKS · 5 of 12 merged ───────────────────────────────────── ⏎ open · r restart ─╮ ╭─ INBOX ────────────────────────── i opens ─╮
│   ID   TITLE                         FILES         WRITER  REVIEW  STATE          │ │ Nothing waits for you.                     │
│   T01  stale target refusal message  review.go     claude  agy     ✓ merged       │ │ Next: final review before merge.           │
│   T02  digest-bound review requests  codec.go      agy     claude  ◎ in review    │ ╰────────────────────────────────────────────╯
│   T03  sub-view external links       review.html   claude  agy     › building     │ ╭─ ACTIVITY · grouped ───────────────────────╮
│   T12  regression matrix sign-off    matrix.md     claude  agy     · waits T03    │ │ 19:30  merge   T08 merged after agy review │
╰───────────────────────────────────────────────────────────────────────────────────╯ │ 19:22  signal  agy-4 slow heartbeat     ×3 │
```

Left column:

- **PLAN**: stages, task and requirement counts, the accepted architecture revision, throughput, progress and ETA.
- **LANES**: one row per worker, read from its heartbeats: model, task (or the review it is doing), step, elapsed time and how long ago it last signalled. A worker silent for two minutes shows as lost.
- **TASKS**: every task with its files, who writes it, who reviews it, and its state (`merged`, `in review`, `building`, `waits T03`, `cancelled`).

Right column:

- **MARSHAL**: the Marshal's provider, model and effort, what it is doing, its context use and limit, and whether its chat is bound to this run, with `input clear` or `input waiting` once the Board has looked at the chat input.
- **WORKERS**: each provider's limit, how many lanes it runs, or `spare`, `at limit`, `not installed`.
- **INBOX**: what waits for you, or "Nothing waits for you." with the next ask.
- **ACTIVITY**: what happened, newest first; repeated lines are folded (`×3`).

### Decision card

When something needs your decision, a card opens over the dimmed Board and the state word becomes `YOUR TURN`:

```text
╭─ ACCEPT ARCHITECTURE · rev 1 ──────────────────────────────────────────────────────────── esc later ─╮
│ 14 parts · 37 links · covers R1–R18 · 5 parts changed by this goal (dashed)                          │
│                                                                                                      │
│  ╭────────────────────╮   ╭────────────────────╮   ╭────────────────────╮   ╭┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄╮   │
│  │ Board              │──▶│ Runtime            │──▶│ Workers            │┄┄▶┆ architecture-review┆   │
│  ╰────────────────────╯   ╰────────────────────╯   ╰────────────────────╯   ╰┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄╯   │
│ No display here, so open this on the machine:  http://127.0.0.1:37667/?token=7964…462b               │
│ ⚠ The Marshal tried to accept this for you at 18:40. Refused: only you can accept.                   │
│                                                                                                      │
│  a  open in browser    c  copy link    k  comment                                                    │
╰──────────────────────────────────────────────────────────────────────────────────────────────────────╯
```

There are three kinds:

| Card | Keys |
|---|---|
| **Accept architecture** | `a` (or `y`) opens the review in your browser, `c` copies its link, `k` says a comment to the Marshal, `Esc` puts the card aside for later. You accept only in the browser; `Enter` on this card tells you so. When no browser can open (no display, for example over SSH), the card shows the review's link. When the Marshal tries to accept for you, the card says it was refused. |
| **Approve plan** (only when your settings ask you) | `Enter` arms the reply and a second `Enter` sends it to the Marshal; any other key cancels. `7` opens the plan, `k` comments, `Esc` later. |
| **Decide** (a request in the inbox) | `a` allow, `d` deny, `Enter` details, `Esc` later. |

`Esc` only steps the card aside: the inbox still lists the decision, and a card opens again when another decision waits.

The requirements, ask, change, "what next?" and guard cards are decision cards over the dimmed Board too, with their own keys on the card; `Esc` puts one off while it waits. The requirements card takes `Enter` to confirm all answers and `e` to edit one.

### Paused card

When work stops and one choice gets it moving again (a provider limit, a provider not signed in, an interrupted lane, a timeout, a lost lease, a stalled or stopped run), the paused card says what happened, which tasks it holds, what is kept, and what `Enter` will do:

```text
╭─ PAUSED · agy Gemini 5h limit ───────────────────────────────────── resets 21:10 ─╮
│ agy-2 and agy-4 hit the agy Gemini 5-hour limit. Their work is kept.              │
│                                                                                   │
│   TASK                         PROGRESS                    KEPT                   │
│   T06 history paging          edits done · tests not run  branch + 3 commits      │
│   T09 dashed-change styles    half edited                 branch + 1 commit       │
│                                                                                   │
│ Enter will: move T06 and T09 to claude sonnet in fresh sessions, keep commits.    │
│                                                                                   │
│  ⏎  move to claude    w  wait until 21:10    d  raw error    x  stop the run      │
╰───────────────────────────────────────────────────────────────────────────────────╯
```

| Key | Action |
|---|---|
| `Enter` | Does what the card says: move the tasks to a provider with room, retry them in fresh sessions, or resume. When no provider has room, it waits and resumes as soon as one has; the work stays saved. |
| `w` | Wait until the limit resets, then resume (only when the stop has a reset time). |
| `d` | Show the raw error. |
| `x` | Stop the run. The run and its work are kept; `/marshal resume` continues it. |

A pause that needs a new plan is a paused card too: `Enter` opens the composer so you can tell the Marshal what to change.

### Marshal chat and split view

The docked chat's pane title is the **MARSHAL CHAT** strip: the provider and model, whether the chat is bound to this run (`● live`), the stage, how many requests it answered and the input state (`input clear` or `input waiting`; nothing before the Board has looked at it), then `[m] switch`. Everything below the strip is drawn by the provider CLI itself.

Beside the chat the Board draws a compact column (Plan, Lanes, Workers, Activity):

```text
    MARSHAL CHAT  ⬡ GPT-6.2-Sol · high  ● live        [m] switch │   BOARD  ·  F11 / Alt+← chat  ·  s: say it to the Marshal
                                                                 │  MARSHAL v0.3.5   project demo   READY                    23:09
  >_ OpenAI Codex                                                │ NOW  none yet  from you nothing
                                                                 │╭─ PLAN ─────────────────────────────────────────────────────────╮
```

---

## Keys

The key bar at the bottom shows the keys for what is on screen. These keys work on the Board.

### Main keys

| Key | Action |
|---|---|
| `s` | Say something to the Marshal: opens the composer; type and press `Enter`. |
| `Enter` | No run yet: open the Marshal chat. On a task: inspect it. On a lane: open its worker. On a card: what the card says. |
| `F11` | Switch between the chat and the Board. In tmux, `Alt+←` moves to the chat and `Alt+→` to the Board. |
| `m` | Choose the Marshal's provider and model (or click the Marshal segment or `[m]`). |
| `a` | Open the current architecture in your browser (also `/marshal architecture`). |
| `y` | Reply to a waiting gate; on the architecture gate it opens the review in the browser. |
| `r` | Restart (retry) the stuck task under the keyboard; confirm with `Enter`. |
| `w` | Choose the model that runs the highlighted task. |
| `v` | On the readiness screen and in the Providers view: check the next provider that has not been checked; `Enter` runs the check. |
| `Space` or `Ctrl+K` | Open the action palette. |
| `/` | Type a command, such as `/marshal settings`. |
| `?` | Every key. |
| `Ctrl+X` | Stop all workers (never the Marshal). |
| `Ctrl+C` | Interrupt; press it twice to leave. |

A letter that is not a Board key starts a message to the Marshal (`to the Marshal › …`); `Esc` cancels it.

### The action palette

`Space` opens the palette with the eight core actions first:

```text
╭─ ACTIONS  type to search all 57 ─────────────────────────────────────────╮
│ > _                                                                      │
├──────────────────────────────────────────────────────────────────────────┤
│ ▶ s       say to the Marshal           p       pause everything          │
│   a       open the architecture        Ctrl+X  stop all workers          │
│   r       restart a task               l       limits and workers        │
│   m       change model or provider     ?       help                      │
│   ── 49 more · type a word, e.g. "retry", "budget", "telemetry"          │
╰─ Enter run · ↑↓ choose · type to filter · Esc close ─────────────────────╯
```

| Palette key | Action |
|---|---|
| `s` | Say to the Marshal |
| `a` | Open the architecture |
| `r` | Restart a stuck task |
| `m` | Change the Marshal's model or provider |
| `p` | Pause everything: every running task pauses and the Marshal starts no new one until you resume (the same as `/pause all`; `/resume` continues) |
| `Ctrl+X` | Stop all workers |
| `l` | Limits and workers: opens the Providers view (`4`) |
| `?` | Help |

Type the key or a word to filter, use the arrows to choose, `Enter` to run, `Esc` to close. An action that cannot run now shows why. `p` and `l` are palette entries: on the Board itself `l` focuses the lanes pane and `p` starts a message.

### Moving around

| Key | Action |
|---|---|
| `j` / `k` or `↑` / `↓` | Move in the focused pane |
| `t` / `i` / `l` | Focus tasks / inbox / lanes |
| `Tab` / `Shift+Tab` | Next / previous pane |
| `Esc` or `q` | Close an inspector, a view or help |

In a task's inspector: `o` shows the worker's pane, `e` toggles all events, `Enter` / `⌫` walk to a child or parent task, `d` / `D` open a diff or the task against its base, `w` chooses its model.

### Inbox

| Key | Action |
|---|---|
| `a` / `A` / `d` / `r` | Allow once, allow for the run, deny, return |
| `i` or `Enter` | Show the request's reason |
| `Enter` | When a run stopped: do what it suggests |
| `x` | Cancel the decision being typed |
| `y` / `u` / `K` | Reply; for changed files: undo, keep |

A hard gate asks you to type its canonical ID, then `Enter`.

### Views

| Key | View |
|---|---|
| `1` | Back to the Board |
| `2` or `h` | Host |
| `3` | Memory |
| `4` | Providers |
| `5` | Verify (after a run starts) |
| `6` | Security (after a run starts) |
| `7` | Plan (after a run starts) |
| `8` or `b` | Architecture |

### Function keys

`F1` help, `F2` review, `F3` diff, `F4` status, `F5` models, `F7` native Codex, `F8` native Claude, `F9` native OpenCode, `F10` update, `F12` native Antigravity. `F6` appears only after an MCP failure and shows what failed.

### Mouse

A left click selects and opens; it never decides on its own.

- A task or lane row: the first click selects it, the second inspects it or shows its worker.
- A provider row on the readiness list: arms its check.
- A card button or a key in the key bar: presses that key.
- An inbox line: selects its request, or arms the gate's reply (a second click sends it).

---

## Narrow terminals and ASCII mode

- **Width**: from 120 columns the Board draws two columns; below that the right column stacks under the left. Board v2 needs at least 80 columns on its own, or 60 beside the docked chat, and 12 rows. A smaller terminal gets the classic narrow Board, which shows the inbox and a notice.
- **Colour**: truecolor when the terminal says so (`COLORTERM=truecolor`), 256 colours otherwise. `marshal tui --theme monochrome` or `--theme high-contrast` chooses another theme; `NO_COLOR` or `marshal tui --no-color` turns colour off.
- **ASCII**: a locale that is not UTF-8 (for example `LC_ALL=C`), or `--no-color`, draws the Board with ASCII boxes and glyphs:

```text
+- PLAN . 12 tasks . 18 requirements ---------------------- accepted rev 1 . 18:52 -+ +- MARSHAL ----------------------- m switch -+
| v goal > v reqs > v design > v arch > v tasks >  build 5/12  > review > merge     | | * claude  opus-5.5 . effort high    in use |
| ###############################..............   done 5 . active 6 . eta 45m       | | context ####......  41% . 82k of 200k      |
+-----------------------------------------------------------------------------------+ +--------------------------------------------+
```

---

## Classic Board for one release

Set `MARSHAL_BOARD=classic` when you start Marshal to keep the classic Board for this release:

```bash
MARSHAL_BOARD=classic marshal
```

The control centre keeps running in its own tmux session, so if it is already running, type `/quit` there first and then start it again with the variable set. Start it without the variable to return to Board v2. The classic Board will be removed in a later release.

---

## Architecture lifecycle

The project has one living architecture. For its baseline, the Marshal describes the whole project, with all parts marked new. Each later goal proposes the next revision: the Marshal keeps existing parts, describes what each connection carries, and ties the requirements to components.

Press **`a`** on the Board, or use **`/marshal architecture`**, to open the current architecture at any time. On the Accept architecture card, **`a`** (or **`y`**) opens the proposal waiting for your acceptance. The browser header shows `rev N → N+1 · proposed` while waiting and `rev N · current` after acceptance.

Solid borders show current parts. Dashed borders and edges show additions connected to the current architecture. Changed parts have a dashed accent outline and a “changed” tag; open a part or connection to see its before and after fields. Removed parts are faded and struck through. Use **Only this goal’s changes** to focus on the proposal while keeping connected existing parts as context.

Comment on the drawing, choose builders if available, and select **Accept architecture** when ready. The Marshal then plans the work and continues the run. **History** lists revisions with their date, goal, run, and who accepted them. Select a revision to inspect its drawing and changes against the preceding revision; return to the latest architecture to comment or accept.

After a run, components whose paths were changed can show **built**. A failed conformance check marks them **differs from the architecture**, so you can see where the implementation needs attention. These marks appear when the run supplies conformance results.
