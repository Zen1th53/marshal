# Changelog

## 0.2.16

### Fixed
- A new revision of an accepted architecture opens in the browser review and can be accepted; a revision missing the fields needed to replace the current one is refused when sent, naming them.
- A refused plan names the project read the Marshal is missing; proposals left over from an ended run are closed.

## 0.2.15

### Fixed
- Opening the architecture before the Marshal sent its project summary says so plainly and asks the Marshal for it; a refused summary tells the Marshal exactly why; a Marshal-saved memory no longer blocks the architecture silently.
- A plan change the Marshal decides is applied once; a too-long reason says so (500-byte limit); `/marshal use-plan` no longer stops with "its workspace didn't match the approved plan"; the rework-limit stop says how to continue; a review refusal names the review.
- When the final check does not accept a run, the Board names the rejected criteria and the reasons.

## 0.2.14

### Fixed
- ULTRA runs whose tasks are done by Antigravity or Claude finish their final check and deliver; the independent checker is now a provider that can name its model (Claude Code) instead of Codex, which always stopped with "REQUIRED_CONTEXT_REVIEW_PENDING". The check itself is unchanged.
- With no independent checker available the run stops before spending a model turn and says to install Claude Code or wait for its limit, then press Enter.
- Final-check stops read in plain words with Enter to retry, also for stops recorded by earlier versions.
- The Board header turns "paused" and back as soon as you pause or resume.
- No false "Verifying Antigravity failed" after "Antigravity verified"; earlier Codex work still loads when another project's Codex session file cannot be read.

### Known limit
- Only Claude Code can be the final ULTRA checker in this build; it must be installed and not be one of the run's workers.

## 0.2.13

### Fixed
- A run paused while a task was working now finishes and delivers; in 0.2.12 it stopped at the final check with nothing delivered. A run that stays paused waits before the final check instead of stopping, "Resume all" resumes a task paused mid-turn, and a run 0.2.12 left stopped continues on Enter.
- The Board header says "paused" while you hold the run.
- An internal verification stop reads "Marshal could not finish checking the run" with Enter to retry, instead of the raw error.
- After planning, Activity says the Marshal approved the plan and the tasks start; no mention of a popup. The busy-Marshal retry note is plain.
- More Marshal tool results in the chat start with a plain line (status, tasks, agents, memory, verification).
- The installer and quick start say `cd /path/to/your/repository && marshal`.

## 0.2.12

### Fixed
- The palette (Space / Ctrl+K) lists only your actions: the Board's keys plus pause all, resume all, doctor and quit; internal menus and do-nothing chords are gone, and no two actions share a key.
- Pause and resume work from the palette for every run of the session; while paused the Marshal starts no new task. A deadlock between pausing and the running engine is fixed.
- Pausing while tasks are working no longer breaks the run: a task that had not started waits, a working task finishes its step, and after resume the Marshal carries on and the run completes. A pause or resume that cannot act names the task or says "one part of the run" instead of an internal id.
- One vitals row across the whole terminal on every screen (in Marshal's own tmux session also above the chat and workers, with a second row for the docked panes' titles); the launch picker, opening and Blocked screen show it too.
- The chat's top row shows the Marshal model, its live limit and a clickable `[m] switch`; narrow widths shorten it in a fixed order and keep the percent left and `[m]`.
- Vitals give way in a fixed order at narrow widths; an unreadable limit always reads "limit unknown" ("?" when narrow); normal vitals text is no longer black; low disk means less than 10 GB or 5% of the disk free, whichever is smaller, everywhere.
- No "Marshal — approval authority" subtitle in the status line, splash or startup output.
- The Board shows MCP (F6) only when it failed; counts read "1 task" / "2 tasks"; it says "planning" while the Marshal plans, also after a restart; views 5, 6 and 7 are off until there is a run and say why; sent-back tasks have their own column; `w` is in the hint bar, which always keeps `?`; Activity shows the Marshal's decisions in plain words; the Providers view lists installed providers.
- The Marshal chat opens with "Hello." / "Hello again."; Marshal's notes start with `[Marshal]` (for example "[Marshal] The run closed: 3 of 4 tasks verified and merged to main."); the Marshal's tool results start with a plain line; Codex tips are off.
- The Marshal names the real controls: Ctrl+X stops all workers, pause and resume are in the palette, the architecture opens on `y`; the shipped build no longer tells it to ask for A in a popup.
- Opening Marshal in an existing repository sets the project up; `marshal doctor` and `marshal status` speak plainly; `/egress`, `/provider`, `/mcp` and `/optimization` answer in plain words; sign-in permission messages say what happens; `marshal verify` names the provider; a task that could not start is tried once more; `/status` and the help footer say whether anonymous telemetry is on.

### Changed
- User docs no longer say you approve the plan or review commands first: the Marshal approves the plan; your acceptance of the architecture is the one gate.

## 0.2.11

### Fixed
- Architecture prep no longer shows a failed project-memory call: any page size is accepted (a larger one reads as the largest page).
- The Marshal can look a task up by the short name the Board shows (for example `greet`) in the current or last run; a shared name lists the choices.
- The "Started a new Git repository" line is also the first Activity line in the TUI, so it is not lost when the TUI opens.

## 0.2.10

### Fixed
- The run-closed notice reaches the Marshal chat (in 0.2.9 it could stay at "sending" and block later messages).
- A self-closing run verifies its result once instead of twice; the final check takes about half as long.
- Left-out secret files are listed in `.gitignore` in Marshal's first commit; `.p12`/`.pfx` added to the default `.gitignore`.
- The start-repository line names both commits; the launch scan ignores Marshal's own files.
- The Marshal says the architecture opens on `y`; no failed context call during intake.
- `marshal doctor`: vendor named once; resource pressure shown as WARN with its reason.

## 0.2.9

### Fixed
- `marshal` and `marshal init` in a folder inside a home-folder Git repository, or in no repository at all, start a separate Git repository in that folder with a first commit (using your own Git identity) and carry on; the first commit gets a default `.gitignore` when the folder has none, never includes secret-looking files (`.env*`, `*.pem`, `*.key`, `id_rsa*`, `*.p12`, `credentials*.json`) and the printed line says how many files were committed and which were left out; the home repository is never touched. Without a Git identity Marshal stops and shows the two `git config --global` commands.
- `marshal setup` no longer reports READY for a folder inside the home folder's repository; it says why and what to do.
- `marshal doctor` without a project prints one reason and one line for the checks that need a project instead of seventeen FAIL lines; the JSON report is unchanged.
- Running Marshal in the home folder or `/` gives a plain message saying to make a project folder and run marshal there.
- The Marshal is told when a run closes and tells you what was delivered.
- No run or operation IDs in commits Marshal makes; `marshal init` names the files it committed; branches of sent-back attempts are removed when a run finishes.
- A second window on the same project no longer fails at launch with "files are damaged" (state check now waits for the other window's database lock).
- Readable requirements card; "a, b" file lists; "final check" says why; usage age shown only when over 30 minutes, the same in vitals and pickers; the Marshal's own chat is not imported as memory; the terminal server starts from `/`.

## 0.2.8

### Fixed
- The Codex and Claude Code Marshal chats start unable to write project files (only `.marshal/` plan files); with OpenCode or Antigravity the Board says when the Marshal changed files and recommends undo.
- A finished run is delivered when the target checkout has uncommitted changes by telling you to stash or undo them, instead of a git instruction; caches like `__pycache__` are ignored; a stopped run shows its real reason.
- Merged run worktrees and branches are cleaned up on close.
- Answered cards close and never take a later `Enter`; `y` on the architecture card gives feedback; the header says "reviewing"; the vitals row keeps the picker's limit reading.

## 0.2.7

v0.2.6 was tagged but never released: a release check stopped it on a Go standard-library vulnerability. Everything planned for 0.2.6 ships here, built with Go 1.26.11.

### Fixed
- Marshal starts on macOS and on Linux without bubblewrap; `marshal setup status` says how work runs instead of reporting a missing sandbox.
- `install.sh` works on macOS (no GNU-only `install -D`).
- The Architecture view, key help and `/egress`, `/sandbox`, `/provider` no longer claim a network broker, credential leases or owner plan approval; the rules line shows the real telemetry state.
- Telemetry registration succeeds on slower connections (900 ms per request).
- A worker still changing files is no longer taken for stalled when its worktree check is slow.
- The Board shows a task as working while its worker runs, a review is no longer shown as stalled, finished tasks stop their clock, and the inbox count matches what is listed.
- A finished run no longer takes an amendment that would re-queue delivered work; the "what next?" card shows beside the chat.
- Usage limits read "% left" everywhere; a limit Marshal cannot read says "limit unknown".
- Built with Go 1.26.11, which fixes a standard-library `net/http` vulnerability (GO-2026-6859).
- Quitting ends Marshal's own tmux session (chat included) and returns you to the shell; each worker uses one window.
- `marshal status` and `marshal tasks` work while the TUI is open; `marshal doctor` marks absent optional plugins as optional.
- Marshal's tool errors to the Marshal are plain sentences; Activity lines and worker commits carry no internal IDs.

### Added
- A project check at every start, with safe fixes and a Blocked screen (`r` checks again).
- A short opening animation instead of the "how a run starts" text.
- A "Who plans?" picker at launch with live usage limits, remembered per project; `m` changes the Marshal later, at a safe point during a run.
- Earlier Claude and Codex sessions for the project load at launch; a launch scan of the project is handed to the Marshal.
- One-key question cards, a requirements card, a mid-run change card (hold affected tasks, apply or keep), a "what next?" card when a run closes, and a "Welcome back" line on resume.
- A usage-limit and machine vitals row on every view; limits on the chat pane and in the Providers view.
- Tasks reroute to the provider with the most limit left; a card when every provider is at its limit.
- Architecture page: click a part to see its files and choose its builder; the plan follows the choice. A revision bar highlights changed parts, and a Design panel shows the design review.
- `Space` / `Ctrl+K` opens one actions palette; `r` retries a stuck task; `w` chooses the model for a task.
- Disk warning at 10 GB or 5% free, whichever is smaller.
- The footer hint bar shows only the actions available now, and each can be clicked; palette rows and help lines can be clicked too. A click never decides: it opens the confirmation.

### Changed
- The architecture is accepted only with the Accept button in the browser (`y` on the gate opens it); typing `accept architecture` in the chat no longer accepts.
- The Marshal approves its own plan (no plan gate); its default execution rights are `read-only`; the plan can mark single tasks strict.
- The Architecture view (`8`) is one status line with `[o]` open; `m` opens the Marshal picker, `F11` moves between chat and Board; `Ctrl+P` is replaced by `Space` / `Ctrl+K`.
- Plain words across the Board, Activity, inbox and errors, with no run or plan IDs; `/status` matches the Board.

### Removed
- The `Ctrl+N` navigation menu, the empty `ev` column, the Memory view's "BY EDITION" panel and the `/ultra` switch.

### Documentation
- README and docs describe the shipped flow and settings defaults; Windows via WSL2; a new lifecycle demo; a setup prompt for coding agents.

## 0.2.5

### Added
- macOS release binaries (darwin amd64 and Apple Silicon arm64); the one-line install works on macOS with no extra step.
- Startup warns, and `marshal doctor` reports, when no supported coding tool (claude/codex/opencode/agy) is installed.

### Documentation
- Telemetry docs show the exact wire format (endpoints, example JSON, every field sent).

## 0.2.4

### Fixed
- Telemetry reports `app_start` at launch on slow connections instead of deferring it a heartbeat interval; per-attempt and offline bounds are unchanged.

## 0.2.3

### Fixed
- Check builds default to `~/.cache/marshal/build` instead of `/tmp`.
- Runtime messages to the Marshal chat no longer get stuck unsent.
- OpenCode harness verification from the Board uses `MARSHAL_OPENCODE_MODEL`.
- Every running lane shows its own live output.

## 0.2.2

### Added
- Live worker output per running lane on the Board.
- Inline task diff in the inspector (`D`), with accept and rework.
- Terminal bell and desktop notifications for NEEDS YOU and run close/stop (`/notify on|off|status`).
- Provider quota in the Board header when the provider records it locally.

### Fixed
- `marshal/` branch prefix (older runs continue), honest Security view and doctor, 112 KiB context packets.

## 0.2.1

First published release of the new Marshal line.

### Fixed
- Marshal role turns time out (default 15 minutes, `MARSHAL_ROLE_TURN_TIMEOUT`) instead of leaving a run in REVIEWING; `/marshal resume` retries.
- The Board flags project files changed outside a run while the Marshal plans, with `[u] undo` and `[K] keep`.
- Built with Go 1.26.10 (standard-library vulnerability fixes).

## 0.2.0

Public release of Marshal: a free, open-source local multi-agent coding orchestrator under AGPL-3.0-only (with commercial licensing options).

### Added
- **Multi-Agent Orchestration Lifecycle**: End-to-end orchestration pipeline from goal intake to verified code closed to `main`.
  - Architecture drafting and interactive operator acceptance gates.
  - Plan decomposition into discrete tasks with verifiable acceptance criteria.
  - Worktree-isolated worker execution preventing branch conflicts.
  - Cross-provider review ensuring no agent grades its own work.
  - Independent verifier validating integrated test suites before closure.
- **The Board (TUI)**: Terminal command center built with tmux.
  - Real-time views for tasks, active worker lanes, activity stream, and docked conversation pane.
  - Focus switching with `F11` and `Alt+←` / `Alt+→`.
  - One-touch operator input with `s` (say) to message the Marshal directly.
  - Harness verification with `v` across providers.
  - Header `NEEDS YOU` gates for operator authorization of architecture and plans.
  - Comprehensive task Inspector with action row buttons (`[a]` approve, `[d]` deny, `[r]` rework, `[x]` cancel, `[o]` open worker pane).
- **Supported Agent Providers**:
  - Anthropic Claude Code (`claude`).
  - OpenAI Codex (`codex`).
  - OpenCode (`opencode`).
  - Google Antigravity (`agy` / `antigravity`).
- **Memory & Model Context Protocol (MCP)**:
  - Local MCP server supporting HTTP loopback (`marshal mcp serve --listen 127.0.0.1:8080`) and stdio (`marshal mcp serve --stdio`).
  - Persistent candidate-reviewed project memory in `.marshal/state.db`.
  - Candidate review interface on the Board (key `3`) and CLI (`marshal memory ...`).
- **Anonymous Telemetry**:
  - Anonymous, opt-out telemetry sent to `https://marshal.blackhat.uz`.
  - Transmits only random installation ID, public key, version, OS/architecture, active mode, and timestamps.
  - Explicit opt-out via `marshal telemetry disable` or `MARSHAL_CLOUD_ENDPOINT=off`.
- **Packaging & Diagnostics**:
  - One-line installer script `install.sh` for Linux amd64 and arm64 with SHA-256 checksum verification.
  - Diagnostic command `marshal doctor` with `--probe-providers` and `--deep` options.
  - Standardized `.marshal/` runtime directory structure with automatic Git exclusion via `.git/info/exclude`.
