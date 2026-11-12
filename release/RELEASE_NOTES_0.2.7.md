# Marshal v0.2.7

v0.2.6 was tagged but never released: a release check stopped it on a Go
standard-library vulnerability. Everything planned for 0.2.6 ships here,
built with Go 1.26.11.

## Fixed

- **Marshal starts on macOS, and on Linux without bubblewrap.** Startup no
  longer refuses to open when `bwrap` is missing. This edition runs workers on
  your computer without a sandbox and never used one. `marshal setup status`
  now says how work runs instead of reporting a missing sandbox.
- **The installer works on macOS.** `install.sh` no longer relies on a
  GNU-only option to create the install directory.
- **The screens no longer claim features this build doesn't have.** The
  Architecture view, the key help and the `/egress`, `/sandbox` and
  `/provider` answers no longer mention a network broker, credential leases or
  owner plan approval. The rules line shows whether anonymous telemetry is
  actually on.
- **Telemetry registers on slow connections.** Each telemetry request now has
  900 ms instead of 750 ms, so a new install on a slower link is counted.
- A worker that is still changing files is no longer mistaken for a stalled
  one when checking its worktree takes a while.
- **The Board agrees with itself during a run.** A task shows as working while
  its worker runs, a task in review is no longer shown as stalled, finished
  tasks stop their clock, and the inbox count matches what is listed.
- **A finished run stays finished.** The Marshal can no longer amend a closed
  run in a way that would queue delivered work again, and the "what next?"
  card now shows beside the chat.
- **Usage limits read the same everywhere**, as "% left". A provider whose
  limit Marshal cannot read shows "limit unknown".
- Built with Go 1.26.11, which fixes a standard-library `net/http`
  vulnerability (GO-2026-6859).
- **Quitting returns you to your shell.** `q` (or `Ctrl+C` twice) now ends
  Marshal's own terminal session, chat included, instead of leaving you in the
  chat pane; a session Marshal did not create is left alone.
- `marshal status` and `marshal tasks` now work while Marshal is open in the
  terminal, and `marshal doctor` marks missing optional plugins as optional.
- Errors from Marshal's own tools reach the Marshal as plain sentences, and
  Activity lines and worker commit messages no longer show internal IDs.

## Added

- **A check at every start.** Marshal checks the project each time it opens
  and fixes safe problems itself (folder permissions on its own files). If
  something is still wrong, a Blocked screen says what, and `r` checks again.
- **A short opening animation** replaces the "how a run starts" text on the
  first screen. Any key skips it.
- **Who plans?** At launch Marshal asks which installed coding tool should act
  as the Marshal, showing how much of each one's usage limit is left. Your
  choice is remembered per project, and the question is skipped when the saved
  choice is still usable. Press `m` on the Board to change the Marshal later;
  during a run the switch waits for a safe point.
- **Earlier work loads by itself.** Earlier Claude and Codex sessions for this
  project are loaded at launch, with one line in Activity saying how many.
- **A project scan for the Marshal.** At launch Marshal looks at the project
  (language, branch, uncommitted changes, open worktrees, unmerged branches,
  earlier runs, limits) and hands that to the Marshal, so it does not have to
  ask you.
- **Cards you answer with one key.** The Marshal's questions show as a card in
  the inbox: `Enter` takes the suggested answer, a letter picks another. The
  requirements show as a card you confirm or edit.
- **Changes in the middle of a run.** When you change the goal during a run,
  the Marshal holds only the tasks it affects and shows a card: apply the
  change, or keep the current goal. Tasks already running finish first.
- **A "what next?" card** when a run you are watching closes: how many tasks
  were verified and merged, then `n` new goal, `d` details, `q` quit.
- **Welcome back.** Reopening Marshal on an active run continues it and shows
  one line with how far it got while you were away.
- **Usage limits on screen.** A row at the top of every view shows each
  provider's usage limit (where its CLI records one) and the machine's CPU,
  temperature, memory, disk and running lanes. The chat pane's top row shows
  the chat model and its limit, and the Providers view fills its limit and
  reset columns from real readings.
- **Reroute on limit.** When a provider hits its limit, waiting tasks move to
  the provider with the most limit left, with one plain line in Activity. When
  every provider is at its limit, a card says so; the work is saved.
- **Choose who builds each part.** In the architecture page in your browser,
  click a part to see what it does, its files, and a Builder picker. The
  Marshal's plan then gives that part's tasks to the provider you chose.
- **The architecture page shows what changed.** A revision bar says how many
  parts changed since the last revision, and changed parts are highlighted.
  When a design review ran, a Design button shows the brief, the reviews and
  how each finding was resolved.
- **Quick actions on the Board.** `Space` or `Ctrl+K` opens one palette of
  every action, with a reason next to any that can't run right now. `r` on a
  stuck task retries it in a fresh session (`Enter` confirms) and continues the
  run. `w` on a task that hasn't started, or is stuck, chooses the model that
  runs it.
- The disk warning now fires when free space drops below 10 GB or 5% of the
  disk, whichever is smaller.
- **The footer shows what you can do now.** The hint bar at the bottom lists
  only the actions available in the current state, and each one can be
  clicked.
- **Every action can be clicked.** Palette rows, help lines, cards, pickers and
  the Board's buttons respond to the mouse as well as to their keys. A click
  never makes a decision for you: on an approval it opens the confirmation,
  and a key confirms.

## Changed

- **You accept the architecture in the browser.** `y` on the NEEDS YOU gate
  (or `o` on the Architecture view) opens the architecture page; its
  **Accept architecture** button is the only way to accept, and the run then
  moves on by itself. Typing `accept architecture` in the chat no longer
  accepts anything. An architecture accepted that way by an earlier version
  shows as a draft again, so you accept it once more in the browser.
- **The Marshal approves its own plan.** There is no plan-approval gate. You
  can stop the run at any time with `Ctrl+X`.
- **The Marshal never edits project files itself.** Its default execution
  rights are now `read-only`; workers do all the edits. The plan can mark
  single risky tasks as strict.
- The Architecture view (`8`) is one status line, with `[o]` to open the page
  in your browser.
- `m` now opens the Marshal picker; `F11` (or `Alt+←` / `Alt+→`) moves between
  the chat and the Board. `Ctrl+P` is replaced by `Space` / `Ctrl+K`.
- Plain words across the Board header, panes, Activity, pauses, inbox entries
  and error messages, with no run or plan IDs.
- `/status` matches what the Board shows.

## Removed

- The `Ctrl+N` navigation menu.
- The always-empty `ev` column and the unused tasks widget on the Board.
- The "BY EDITION" panel in the Memory view.
- The `/ultra` switch. `/ultra stop` quietly removed cross-review and the
  verifier, so this build no longer offers it.

## Documentation

- README and docs describe the flow this build ships and its settings
  defaults.
- Windows is supported through WSL2; the docs and badge say so.
- A new demo recording shows the whole run, from plan to merge.
- A setup prompt you can give a coding agent to install and check Marshal.

## Known issues

- When every installed provider is a worker in the same run, no independent
  verifier is left and the run cannot close. Keep one provider out of the
  worker list for now.
- On macOS, the host-watch CPU/temperature panel reads Linux counters and
  shows nothing; the rest of Marshal works.
- Claude's usage limit is read from what Claude Code last saved, so it can be
  old. agy and opencode report no limit; Marshal shows "limit unknown" for them.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

The same line now works on macOS (Intel and Apple Silicon), and the macOS
binary starts. Existing installs: press `F10` in the TUI or run
`marshal update install`.
