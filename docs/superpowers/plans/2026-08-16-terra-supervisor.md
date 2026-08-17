# MARSHAL TERRA Persistent Supervisor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a persistent single-worker TERRA supervisor that invokes Codex non-interactively until MARSHAL is complete, queues overlapping supervisor invocations with an OS lock, retries transient runner failures without cancelling live work, and fails closed on real blockers/security findings.

**Architecture:** A standard-library Python supervisor owns a blocking `fcntl.flock`, writes durable status/log files under `.marshal-supervisor`, and launches exactly one `codex exec` subprocess at a time. Codex returns a schema-validated JSON final result; the supervisor loops on `CONTINUE`, exits zero on `ALL_DONE`, exits blocked on task/security blockers, and retries runner/process/malformed-result failures with capped backoff.

**Tech Stack:** Python 3.11+ standard library, `unittest`, Git, Codex CLI non-interactive `exec` mode.

## Global Constraints

- Repository Git identity must be `Zen1th53 <extreme29@proton.me>`.
- Never use `codex --yolo` or `--dangerously-bypass-approvals-and-sandbox`.
- Use Codex `workspace-write` sandboxing for unattended workers.
- One Codex subprocess maximum at a time.
- A second supervisor invocation waits on the same file lock instead of cancelling or overlapping the active worker.
- No secret/token/API key material may be persisted in prompts, logs, status files, or repository content.
- Structured HIGH/CRITICAL findings and explicit `BLOCKED` are fail-closed terminal states.
- Runner/auth/quota/process/malformed-output failures are retried with capped backoff; they are never reported as successful build checkpoints.
- `ALL_DONE` is accepted only from schema-valid output with zero HIGH and zero CRITICAL findings.
- Supervisor must have an operator STOP sentinel even though normal execution continues until completion.

---

### Task 1: Result Contract and Single-Worker Queue

**Files:**
- Create: `tools/terra_supervisor.py`
- Create: `tools/terra_supervisor_result.schema.json`
- Create: `tools/tests_v6/test_terra_supervisor.py`

**Interfaces:**
- Consumes: repository path, TERRA path, Codex executable path, state directory.
- Produces: `SupervisorConfig`, `WorkerResult`, `run_supervisor(config) -> int`, blocking lock semantics, status JSON, `ALL_DONE` sentinel.

- [ ] **Step 1: Write failing queue/result tests**

Create tests that launch the supervisor with a fake Codex executable and assert:

```python
self.assertEqual(proc.returncode, 0)
self.assertTrue((state_dir / "ALL_DONE").exists())
self.assertEqual(fake_calls.read_text().count("CALL\n"), 2)
```

The fake worker returns `CONTINUE` on its first call and `ALL_DONE` on its second call. Add a concurrent-process test where process B cannot enter the fake worker until process A releases the supervisor lock.

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
python3 -m unittest tools.tests_v6.test_terra_supervisor -v
```

Expected: import/file failures because `tools/terra_supervisor.py` and schema do not exist.

- [ ] **Step 3: Implement minimal result contract and queue loop**

Implement:

```python
@dataclass(frozen=True)
class WorkerResult:
    status: str
    current_epic: str
    current_atomic_task: str
    commit: str
    summary: str
    high_findings: int
    critical_findings: int


def run_supervisor(config: SupervisorConfig) -> int:
    # create state dirs
    # acquire blocking fcntl.flock
    # set/verify Git identity
    # invoke one worker
    # parse schema-valid final JSON
    # CONTINUE -> next iteration
    # ALL_DONE with zero findings -> sentinel + exit 0
    # BLOCKED or findings -> exit blocked
```

Create a JSON Schema with `additionalProperties: false`, required fields matching `WorkerResult`, `status` enum `CONTINUE|BLOCKED|ALL_DONE`, and non-negative integer finding counts.

- [ ] **Step 4: Run tests and verify GREEN**

Run:

```bash
python3 -m unittest tools.tests_v6.test_terra_supervisor -v
```

Expected: queue/result tests PASS.

- [ ] **Step 5: Verify Git identity and commit**

Run:

```bash
git diff --check
git config user.name
git config user.email
```

Commit:

```bash
git add tools/terra_supervisor.py tools/terra_supervisor_result.schema.json tools/tests_v6/test_terra_supervisor.py
git commit -m "feat: add persistent TERRA supervisor"
```

---

### Task 2: Retry, Stop, Logging, and Safe Codex Invocation

**Files:**
- Modify: `tools/terra_supervisor.py`
- Modify: `tools/tests_v6/test_terra_supervisor.py`
- Create: `docs/terra-supervisor.md`

**Interfaces:**
- Consumes: Task 1 supervisor loop.
- Produces: capped backoff, STOP sentinel, per-iteration JSONL/final logs, safe default Codex invocation, workstation launch instructions.

- [ ] **Step 1: Write failing reliability/security tests**

Add tests proving:

```python
# transient worker failure is retried
self.assertGreaterEqual(call_count, 2)

# explicit BLOCKED exits nonzero without another worker call
self.assertEqual(call_count, 1)
self.assertNotEqual(proc.returncode, 0)

# HIGH/CRITICAL override a claimed CONTINUE/ALL_DONE
self.assertNotEqual(proc.returncode, 0)

# STOP prevents another worker launch
self.assertEqual(call_count_after_stop, 1)

# generated command includes workspace-write and excludes dangerous bypass flags
self.assertIn("workspace-write", argv)
self.assertNotIn("--yolo", argv)
self.assertNotIn("--dangerously-bypass-approvals-and-sandbox", argv)
```

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
python3 -m unittest tools.tests_v6.test_terra_supervisor -v
```

Expected: failures for retry/STOP/logging/safe-command behavior not implemented yet.

- [ ] **Step 3: Implement reliability and safe worker command**

Implement capped exponential backoff:

```python
delay = min(config.max_backoff_seconds, config.initial_backoff_seconds * (2 ** failures))
```

Reset failure count after any schema-valid worker result. Use injectable sleep in unit tests.

Build the real worker command without shell interpolation:

```python
[
    codex,
    "--ask-for-approval", "never",
    "exec",
    "--cd", str(repo),
    "--sandbox", "workspace-write",
    "--json",
    "--output-last-message", str(final_path),
    "--output-schema", str(schema_path),
    "-",
]
```

Feed the durable TERRA objective on stdin. Write raw Codex JSONL stdout/stderr to per-iteration logs without echoing environment values or credentials.

Check STOP before each worker launch and during retry waits. Preserve a running worker: do not terminate it merely because another supervisor instance exists.

- [ ] **Step 4: Document workstation launch**

Document:

```bash
nohup python3 tools/terra_supervisor.py \
  --repo /home/zen1th53/Desktop/codex/marshal \
  --terra /home/zen1th53/Desktop/codex/MARSHAL-TERRA-v3 \
  > /home/zen1th53/Desktop/codex/marshal-supervisor.out 2>&1 &
```

Also document `status`, `STOP`, logs, and restart behavior. State explicitly that the supervisor requires a locally installed/authenticated Codex CLI and is not a replacement for an LLM runtime.

- [ ] **Step 5: Full verification and commit**

Run:

```bash
python3 -m unittest tools.tests_v6.test_terra_supervisor -v
python3 -m unittest discover -s tools/tests_v6 -p 'test_*.py'
python3 tools/release_verify.py . distribution/PACK-MANIFEST.json
```

Regenerate `distribution/PACK-MANIFEST.json` only if tracked supervisor files legitimately make it stale, then verify deterministic regeneration.

Run:

```bash
git diff --check
git status --short
```

Commit verified changes as:

```bash
git add tools/terra_supervisor.py tools/tests_v6/test_terra_supervisor.py docs/terra-supervisor.md distribution/PACK-MANIFEST.json
git commit -m "test: harden TERRA supervisor recovery"
```
