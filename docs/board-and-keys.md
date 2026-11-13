# The Board and Keyboard Shortcuts

Marshal's Board shows the run, tasks, inbox, worker lanes and activity. A vitals row appears at the top of every screen with provider limits and host information. The footer and `/status` report whether anonymous telemetry is on or off; `marshal telemetry disable` turns it off.

## Chat and the Board

- **`F11`** switches between the chat and Board. In tmux, `Alt+←` and `Alt+→` also move between them.
- **`s`** opens the message composer from the Board. Type a message and press `Enter`.
- **`m`** or click **`[m]`** to choose the Marshal model.
- Highlight a task and press **`w`** to choose its model.
- **`y`** opens the architecture in your browser when the architecture is waiting for you. Accept it in the browser.
- **`Ctrl+X`** stops all workers, not the Marshal.

The Marshal fills a requirements card before the architecture gate. Press **`Enter`** to confirm all answers or **`e`** to edit an answer. The architecture acceptance is the owner gate. The Marshal then plans and approves the plan itself; there is no plan approval popup.

## Views

| Key | View |
|---|---|
| `1` | Board |
| `2` or `h` | Host |
| `3` | Memory |
| `4` | Providers |
| `5` | Verify (available after a run starts) |
| `6` | Security (available after a run starts) |
| `7` | Plan (available after a run starts) |
| `8` or `b` | Architecture |

## Board keys

| Key | Action |
|---|---|
| `j` / `k` or arrows | Move in the focused pane |
| `t` / `i` / `l` | Focus tasks / inbox / lanes |
| `Tab` / `Shift+Tab` | Move between panes |
| `Enter` | Inspect a task or open a lane's worker |
| `d` / `D` | Open a diff / show the selected task against its base |
| `r` | Retry a stuck task; then confirm with `Enter` |
| `Esc` or `q` | Close an inspector or view |
| `?` | Open the key help |

The action palette opens with **`Space`** or **`Ctrl+K`**. It lists current Board actions and the available run controls: pause all, resume all, doctor and quit. When paused, the Board header says **`paused`**. Type in the palette to filter, use the arrows to choose, and press `Enter` to run an action.

**`F6`** is shown only after an MCP failure. It opens the failure details.
