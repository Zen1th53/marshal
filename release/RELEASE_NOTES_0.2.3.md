# Marshal v0.2.3

## Fixed

- **Check builds no longer use /tmp.** Verification builds default to
  `~/.cache/marshal/build` (`MARSHAL_BUILD_DIR` still overrides), so a small or
  quota-limited `/tmp` no longer fails every check.
- **Runtime messages reach the Marshal chat.** Decision requests are pasted with
  bracketed paste and confirmed; they no longer stay stuck as "[Pasted text]".
- **OpenCode verification from the Board.** `v` uses `MARSHAL_OPENCODE_MODEL`,
  and the error says how to pass a model (`/harness verify opencode <model>`).
- **Live output for every lane.** Each running lane shows its own output rows,
  not only the first one.

## Docs

- New README demo: Codex, Claude, OpenCode and Antigravity build four modules
  in parallel, each reviewed by a different model.

## Known issues

- When every installed provider is a worker in the same run, no independent
  verifier is left and the run cannot close. Keep one provider out of the
  worker list for now.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```
