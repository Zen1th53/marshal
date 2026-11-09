# Marshal v0.2.2

Built by Marshal on its own repository: the Marshal (Claude) planned these
features, and Codex, Claude and Antigravity workers built them in parallel
under cross-provider review.

## New on the Board

- **Live worker output.** A running lane shows the last lines of its worker,
  updated as it works.
- **Inline task diff.** In the inspector, `D` shows the task's changes against
  its base, with accept and rework reachable from there.
- **Attention alerts.** When the Board needs you (NEEDS YOU) or a run closes or
  stops, Marshal rings the terminal bell and, when `notify-send` exists, sends
  a desktop notification. Turn them off with `/notify off`.
- **Provider limits.** The header shows a provider's remaining quota when its
  CLI records it locally (Codex 5-hour and weekly); otherwise it says
  `unknown` and never guesses.

## Fixed

- Git branches in your project are named `marshal/…`; runs started with the
  old prefix continue.
- The Security view and `marshal doctor` describe host execution accurately.
- Large repositories no longer stop a run on the context-packet limit
  (raised from 64 KiB to 112 KiB).
- The Board flags project files changed outside a run while the Marshal plans,
  with undo and keep.

## Known issues

- With several running lanes, only the first lane's output fits on a small
  Board.
- Check builds default to `/tmp`; set `MARSHAL_BUILD_DIR` to a directory with
  room if `/tmp` is small or quota-limited.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```
