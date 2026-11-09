# Changelog

## 0.2.2

### Added
- Live worker output per running lane on the Board.
- Inline task diff in the inspector (`D`), with accept and rework.
- Terminal bell and desktop notifications for NEEDS YOU and run close/stop (`/notify on|off|status`).
- Provider quota in the Board header when the provider records it locally.

### Fixed
- `marshal/` branch prefix (older runs continue), honest Security view and doctor, 112 KiB context packets, Board guard for files changed outside a run.

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
