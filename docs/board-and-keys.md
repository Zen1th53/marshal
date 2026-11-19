# The Board and Keyboard Shortcuts

Marshal's Board shows the run, tasks, inbox, worker lanes and activity. A vitals row appears at the top of every screen with provider limits and host information. The footer and `/status` report whether anonymous telemetry is on or off; `marshal telemetry disable` turns it off.

## Chat and the Board

- **`F11`** switches between the chat and Board. In tmux, `Alt+←` and `Alt+→` also move between them.
- **`s`** opens the message composer from the Board. Type a message and press `Enter`.
- **`m`** or click **`[m]`** to choose the Marshal model.
- Highlight a task and press **`w`** to choose its model.
- **`a`** opens the current architecture in your browser at any time; `/marshal architecture` does the same.
- **`y`** opens the architecture in your browser when the architecture is waiting for you. Accept it in the browser.
- **`v`** verifies the next unverified provider (in the Providers view, the selected one); press `Enter` to run the check.
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

## Architecture lifecycle

The project has one living architecture. For its baseline, the Marshal describes the whole project, with all parts marked new. Each later goal proposes the next revision: the Marshal keeps existing parts, describes what each connection carries, and ties the requirements to components.

Press **`a`** on the Board, or use **`/marshal architecture`**, to open the current architecture at any time. **`y`** opens a proposal waiting for your acceptance. The browser header shows `rev N → N+1 · proposed` while waiting and `rev N · current` after acceptance.

Solid borders show current parts. Dashed borders and edges show additions connected to the current architecture. Changed parts have a dashed accent outline and a “changed” tag; open a part or connection to see its before and after fields. Removed parts are faded and struck through. Use **Only this goal’s changes** to focus on the proposal while keeping connected existing parts as context.

Comment on the drawing, choose builders if available, and select **Accept architecture** when ready. The Marshal then plans the work and continues the run. **History** lists revisions with their date, goal, run, and who accepted them. Select a revision to inspect its drawing and changes against the preceding revision; return to the latest architecture to comment or accept.

After a run, components whose paths were changed can show **built**. A failed conformance check marks them **differs from the architecture**, so you can see where the implementation needs attention. These marks appear when the run supplies conformance results.
