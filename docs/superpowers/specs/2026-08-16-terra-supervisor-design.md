# MARSHAL TERRA Persistent Supervisor Design

## Goal

Provide a single persistent local supervisor that keeps MARSHAL TERRA implementation moving until all dependency-valid work is complete, without relying on ChatGPT scheduled tasks or cron-style overlapping invocations.

## Architecture

The supervisor is a small Python standard-library program under `tools/terra_supervisor.py`. It owns one OS file lock for the lifetime of the process. While holding that lock it repeatedly invokes a single non-interactive Codex worker, waits for that worker to exit, validates the worker's structured final result, records a durable status heartbeat, then either immediately starts the next worker, waits with bounded backoff after transient runner failures, stops fail-closed on a real blocker/high/critical finding, or exits successfully only when the worker reports that all TERRA work is complete and verified.

A second supervisor invocation never kills or races the first. It blocks on the same file lock and therefore acts as a queue entry. Once the active supervisor releases the lock, the waiting invocation rechecks repository state and can either continue or observe the completed sentinel and exit.

## Worker Contract

The default worker is `codex exec` in non-interactive mode. The supervisor feeds a fixed durable objective plus live repository/TERRA paths through stdin. Codex must:

- inspect current repository/TERRA truth before acting;
- continue only dependency-valid next atomic tasks;
- preserve strict A01→A10 order inside each epic;
- use test-first implementation and fresh verification;
- keep all commits authored and committed as `Zen1th53 <extreme29@proton.me>`;
- never fabricate Gemini/test/review results;
- never force-push or rewrite verified history;
- stop and report `BLOCKED` for unresolved HIGH/CRITICAL findings or genuine blockers;
- report `CONTINUE` after a verified checkpoint when more work remains;
- report `ALL_DONE` only after T01–T55 completion and final ZIP/bundle/report verification.

The final response is validated against `tools/terra_supervisor_result.schema.json` and captured with Codex `--output-last-message`.

## Process and Queue Semantics

- Lifetime lock: `.marshal-supervisor/supervisor.lock`.
- Blocking lock acquisition is the queue. No duplicate worker runs.
- One Codex subprocess maximum at any time.
- No fixed 20-minute timer is needed: the next worker starts as soon as the previous worker finishes.
- Manual stop is available through `.marshal-supervisor/STOP` for operator safety.
- Completion sentinel: `.marshal-supervisor/ALL_DONE`.
- Current status: `.marshal-supervisor/status.json`.
- Per-iteration logs: `.marshal-supervisor/logs/<iteration>-<timestamp>.jsonl` plus final response JSON.

## Failure Model

Codex/transport/auth/quota/process failures are treated as runner failures, not successful build results. The supervisor keeps running and retries with capped exponential backoff. It never cancels an already running worker.

Structured `BLOCKED`, nonzero HIGH findings, or nonzero CRITICAL findings are security/task blockers and stop the supervisor fail-closed instead of retrying blindly.

Malformed/missing structured final output is treated as a runner failure and retried with backoff.

## Security

The supervisor uses Codex `workspace-write` sandboxing and never uses `--yolo`. It does not copy or print credentials. It passes no secrets in prompts. It configures the repository-local Git identity before starting workers and verifies identity on every loop.

Only repository and configured TERRA directories are writable/visible through the worker command. The supervisor itself does not merge, rewrite, or fabricate repository state.

## Testing

Standard-library `unittest` coverage uses a fake Codex executable to prove:

- only one worker runs at a time;
- a second supervisor waits for the file lock;
- `CONTINUE` immediately advances to the next worker;
- `ALL_DONE` writes the completion sentinel and exits zero;
- `BLOCKED` exits fail-closed;
- HIGH/CRITICAL findings fail closed regardless of claimed status;
- transient worker failure is retried rather than terminating;
- malformed result is retried;
- STOP sentinel halts cleanly;
- Git identity is set to Zen1th53;
- no dangerous Codex bypass flag is used.

## Deployment

The supervisor is intended to run on the user's workstation where Codex CLI authentication already exists. It can be launched from Remote Codex with `nohup` or a terminal multiplexer. The current ChatGPT sandbox has no local `codex` executable, so it can validate the supervisor with a fake worker but cannot itself use this script to invoke Codex after the chat turn ends.
