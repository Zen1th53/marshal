# Local Runtime V1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver a real Linux local SLAVES runtime whose CLI controls a Unix-socket daemon backed by transactional SQLite, enforced policy, task worktrees, a bwrap worker, a real Codex adapter, and commit-bound evidence.

**Architecture:** A thin `slaves` CLI talks HTTP/JSON over a mode-`0600` Unix socket to one daemon. The daemon owns all live coordination writes in SQLite and composes focused identity, scheduler, policy, worktree, sandbox, worker, event, artifact, and verification packages. Bootstrap init is the only pre-daemon database writer.

**Tech Stack:** Go 1.25 baseline, standard library CLI/HTTP/process management, `modernc.org/sqlite v1.56.0`, `go.yaml.in/yaml/v3 v3.0.5`, Git CLI, bubblewrap, Codex CLI, existing Python conformance tooling.

## Global Constraints

- Preserve `PACK-VERSION.yaml` pack version `6.0.0` and runtime specification version `1.0.0`.
- Set executable runtime implementation version to `0.1.0`.
- Keep SQLite as the only canonical live coordination writer.
- Never expose a TCP listener, raw shell-string endpoint, hidden direct-mode write, fake sandbox state, or automatic lease theft.
- Never reset or delete an unexpected dirty worktree.
- Never transition a worker result directly to merged, QA PASS, AppSec PASS, or complete.
- Do not modify `TORVALDS.md`.
- Every production behavior follows a witnessed Red → Green → Refactor cycle.
- Every task below ends with an independently building and tested Conventional Commit.

---

## File Map

- `cmd/slaves/main.go`: process entrypoint and exit-code handoff.
- `internal/cli/cli.go`: argument parsing, human/JSON rendering, daemon client calls.
- `internal/api/server.go`: local HTTP/JSON routes and typed response envelopes.
- `internal/api/client.go`: Unix-socket HTTP client.
- `internal/app/runtime.go`: use-case orchestration; no transport formatting.
- `internal/model/*.go`: stable domain records, enums, typed errors, request/result values.
- `internal/store/sqlite.go`: connection pragmas, transaction helpers, integrity checks.
- `internal/store/migrations.go`: ordered embedded schema migrations.
- `internal/store/tasks.go`: task import/query/claim/release and scheduler queries.
- `internal/store/identity.go`: agent/session/heartbeat lifecycle.
- `internal/store/policy_records.go`: approvals, findings, and memory writes.
- `internal/store/evidence.go`: events, runs, artifacts, and verifications.
- `internal/policy/engine.go`: strict CAPABILITIES loading and semantic decisions.
- `internal/worktree/manager.go`: real Git branch/worktree creation and validation.
- `internal/sandbox/backend.go`: isolation capability contract.
- `internal/sandbox/bwrap_linux.go`: bubblewrap probe and command envelope.
- `internal/adapter/adapter.go`: existing adapter-contract-shaped runtime interface.
- `internal/adapter/codex/codex.go`: native Codex probe and JSONL execution.
- `internal/worker/manager.go`: process-group, timeout, lifecycle, and evidence capture.
- `internal/doctor/doctor.go`: real environment probes and PASS/DEGRADED/FAIL aggregation.
- `internal/integration/*_test.go`: temporary-repository end-to-end and adversarial tests.

---

### Task 1: Bootstrap, Models, and Deterministic SQLite Schema

**Files:**
- Create: `go.mod`
- Create: `go.sum`
- Create: `cmd/slaves/main.go`
- Create: `internal/model/errors.go`
- Create: `internal/model/entities.go`
- Create: `internal/project/layout.go`
- Create: `internal/store/sqlite.go`
- Create: `internal/store/migrations.go`
- Create: `internal/testutil/testgit/git.go`
- Test: `internal/store/sqlite_test.go`
- Test: `internal/project/layout_test.go`
- Modify: `.gitignore`
- Modify: `memory/DEPENDENCIES.md`

**Interfaces:**
- Produces: `project.Discover(root string) (project.Layout, error)`
- Produces: `store.Open(ctx context.Context, dbPath string) (*store.Store, error)`
- Produces: `(*store.Store).Migrate(ctx context.Context) error`
- Produces: `(*store.Store).InitProject(ctx context.Context, model.Project) error`
- Produces: `model.CodeError{Code, Message, Err}` and exit-code mapping.
- Produces: test-only `testgit.New(t testing.TB) *testgit.Repository` with a
  real initialized Git repository, local test identity, and initial commit.

- [ ] **Step 1: Write failing bootstrap tests**

Define tests that name these breaks: init outside Git succeeds, a second init
duplicates the project, foreign keys stay disabled, or a partial migration is
accepted.

```go
func TestInitProjectIsIdempotent(t *testing.T) {
    repo := testgit.New(t)
    layout, err := project.Discover(repo.Path())
    requireNoError(t, err)
    st, err := store.Open(context.Background(), layout.Database)
    requireNoError(t, err)
    t.Cleanup(func() { st.Close() })

    p := model.Project{ID: "PROJECT-local", Repository: repo.Path(), DefaultBranch: "main", PackVersion: "6.0.0"}
    requireNoError(t, st.Migrate(context.Background()))
    requireNoError(t, st.InitProject(context.Background(), p))
    requireNoError(t, st.InitProject(context.Background(), p))

    if got := queryInt(t, st.db, "SELECT count(*) FROM projects"); got != 1 {
        t.Fatalf("project rows = %d, want 1", got)
    }
}
```

- [ ] **Step 2: Run tests and witness RED**

Run: `go test ./internal/project ./internal/store -run 'Test(Discover|Init|Migration|Foreign)' -v`

Expected: compilation fails because the packages and APIs do not exist.

- [ ] **Step 3: Add the module and minimum production implementation**

Use:

```go
module github.com/Zen1th53/slaves

go 1.25.0

require (
    go.yaml.in/yaml/v3 v3.0.5
    modernc.org/sqlite v1.56.0
)
```

`Open` must configure each connection with foreign keys, WAL, and a bounded
busy timeout, restrict the pool to a tested single-writer shape, then verify
`PRAGMA foreign_keys`. Migration 1 creates `schema_migrations` plus all
contract tables and the required `worker_runs` and `verifications` tables.
Include CHECK constraints for statuses/roles/risks, foreign keys, task revision
non-negativity, unique task dependencies, and:

```sql
CREATE UNIQUE INDEX one_active_lease_per_task
ON leases(task_id) WHERE status = 'active';
```

`project.Discover` uses `git rev-parse --show-toplevel`,
`git symbolic-ref --short HEAD`, and `git rev-parse HEAD`; it creates no
files. `layout.Ensure` creates `.slaves` and `artifacts/worktrees/logs`
with `0700`.

Add exactly these generated-state ignores:

```gitignore
.slaves/
```

Record both direct dependencies in the dependency ledger with version,
upstream, license, reason, removal path, and review date.

- [ ] **Step 4: Run GREEN and schema inspection**

Run:

```bash
go mod tidy
go test ./internal/project ./internal/store -v
go test ./...
go vet ./...
```

Expected: all commands exit 0; tests assert schema version 1, every required
table, foreign keys enabled, repeat init stable, and migration rollback on
failure.

- [ ] **Step 5: Refactor and commit**

Review that SQL is only in `migrations.go`, connection policy only in
`sqlite.go`, and no generic repository abstraction was introduced.

```bash
git add go.mod go.sum cmd/slaves internal/model internal/project internal/store internal/testutil .gitignore memory/DEPENDENCIES.md
git commit -m "feat(runtime): add canonical SQLite state store"
```

---

### Task 2: Task Import, Identity Sessions, Scheduler, and Atomic Leases

**Files:**
- Create: `internal/model/task.go`
- Create: `internal/model/identity.go`
- Create: `internal/store/tasks.go`
- Create: `internal/store/identity.go`
- Create: `internal/scheduler/scheduler.go`
- Test: `internal/store/tasks_test.go`
- Test: `internal/store/identity_test.go`
- Test: `internal/scheduler/scheduler_test.go`

**Interfaces:**
- Produces: `store.ImportTasks(ctx, []model.Task) (model.ImportResult, error)`
- Produces: `store.RegisterAgent(ctx, model.Agent) error`
- Produces: `store.StartSession(ctx, model.Session) error`
- Produces: `store.Heartbeat(ctx, sessionID string, at time.Time) error`
- Produces: `store.TerminateSession(ctx, sessionID, status string, expectedRevision int64) error`
- Produces: `store.ClaimTask(ctx, model.ClaimRequest) (model.Lease, error)`
- Produces: `store.ReleaseTask(ctx, model.ReleaseRequest) error`
- Produces: `scheduler.Ready(ctx) ([]model.Task, error)`

- [ ] **Step 1: Write task import and identity RED tests**

Use literal JSON fixtures through a strict decoder. Tests must prove object and
array import, unknown-field rejection, all-or-nothing missing-dependency
failure, idempotent match, divergent-ID conflict, immutable agent role,
session/task binding, heartbeat persistence, and stale session termination.

```go
func TestImportTasksRollsBackWhenDependencyIsMissing(t *testing.T) {
    st := newStore(t)
    tasks := []model.Task{
        {ID: "TASK-002", Title: "consumer", Status: model.TaskReady, Risk: model.R1, Revision: 0, Dependencies: []string{"TASK-404"}},
    }
    if _, err := st.ImportTasks(context.Background(), tasks); !errors.Is(err, model.ErrConflict) {
        t.Fatalf("error = %v, want conflict", err)
    }
    if got := countTasks(t, st); got != 0 {
        t.Fatalf("tasks = %d, want rollback to 0", got)
    }
}
```

- [ ] **Step 2: Run import/identity tests and witness RED**

Run: `go test ./internal/store -run 'Test(Import|Register|Session|Heartbeat)' -v`

Expected: compilation fails on missing task and identity operations.

- [ ] **Step 3: Implement strict import and session lifecycle**

Decode task JSON with `json.Decoder.DisallowUnknownFields`, reject trailing
tokens, validate the exact schema enums/patterns, and canonicalize dependency
order only for comparison. Never rewrite user JSON.

Agent IDs and session IDs use cryptographically random 128-bit values encoded
with the contract prefixes. Registration validates a closed role enum.
Heartbeat updates liveness and revision only. Termination never releases or
reassigns a task implicitly.

- [ ] **Step 4: Write and witness concurrent-claim RED**

```go
func TestConcurrentClaimHasExactlyOneWinner(t *testing.T) {
    st := readyTaskStore(t, "TASK-001")
    const contenders = 32
    var wins atomic.Int32
    var conflicts atomic.Int32
    var wg sync.WaitGroup
    for i := 0; i < contenders; i++ {
        wg.Add(1)
        go func(i int) {
            defer wg.Done()
            req := claimRequest(t, st, "TASK-001", i, 0)
            _, err := st.ClaimTask(context.Background(), req)
            switch {
            case err == nil:
                wins.Add(1)
            case errors.Is(err, model.ErrConflict):
                conflicts.Add(1)
            default:
                t.Errorf("claim: %v", err)
            }
        }(i)
    }
    wg.Wait()
    if wins.Load() != 1 || conflicts.Load() != contenders-1 {
        t.Fatalf("wins=%d conflicts=%d", wins.Load(), conflicts.Load())
    }
}
```

Run: `go test ./internal/store -run TestConcurrentClaimHasExactlyOneWinner -count=1 -v`

Expected: FAIL because claim is absent.

- [ ] **Step 5: Implement atomic claim/release and deterministic ready query**

Claim starts an immediate write transaction, compares expected revision,
requires `ready`, checks every hard dependency is `merged`, inserts the
active lease, updates ownership/status/revision, binds the session, and inserts
`TASK_CLAIMED` before commit. Map constraint/busy/stale outcomes to conflict;
do not retry a losing claim.

Release validates lease owner and revision, ends the lease, clears task/session
binding, returns task to `ready` unless an explicit blocked reason is supplied,
increments revision, and inserts `TASK_RELEASED`.

`Ready` orders dependency-unblocking count descending, task ID ascending.

- [ ] **Step 6: Run GREEN, race, and commit**

```bash
go test ./internal/store ./internal/scheduler -count=1 -v
go test -race ./internal/store ./internal/scheduler -count=10
go test ./...
git add internal/model internal/store internal/scheduler
git commit -m "feat(runtime): add task leases and identity sessions"
```

Expected: exactly one claim winner in every race iteration and all prior tests
green.

---

### Task 3: Enforced Policy, Approval Validation, Finding Ownership, and Memory Secret Rejection

**Files:**
- Create: `internal/model/policy.go`
- Create: `internal/policy/engine.go`
- Create: `internal/store/policy_records.go`
- Test: `internal/policy/engine_test.go`
- Test: `internal/store/policy_records_test.go`

**Interfaces:**
- Produces: `policy.Load(path string) (*policy.Engine, error)`
- Produces: `(*policy.Engine).Decide(model.PolicyInput) model.PolicyDecision`
- Produces: `store.ValidateApproval(ctx, model.ApprovalUse) (model.Approval, error)`
- Produces: `store.ConsumeApproval(ctx, approvalID string, expectedRevision int64) error`
- Produces: `store.TransitionFinding(ctx, model.FindingTransition) error`
- Produces: `store.Remember(ctx, model.MemoryRecord) error`

- [ ] **Step 1: Write policy RED tests**

Table-drive literal expectations for every exposed semantic operation. Include
filesystem/task scope, network-required Codex use, production mutation,
history rewrite, git push, secret read, external upload, deploy, and
destructive operation. Assert the returned decision, rule, and non-secret
reason.

```go
func TestDeniedHistoryRewriteCannotExecute(t *testing.T) {
    eng := loadPolicy(t)
    called := false
    err := policy.Enforce(eng, historyRewriteInput(), func() error {
        called = true
        return nil
    })
    if !errors.Is(err, model.ErrPolicyDenied) || called {
        t.Fatalf("err=%v called=%v", err, called)
    }
}
```

Run: `go test ./internal/policy -v`

Expected: compilation fails because the engine is absent.

- [ ] **Step 2: Implement strict policy loading and enforcement**

Use YAML decoder `KnownFields(true)`, validate required default keys and all
closed role names, normalize underscore YAML keys to semantic operation
constants in one explicit map, and reject unknown capability values.
`Enforce` calls the operation only after ALLOW or valid approved state.
Material DENY/REQUIRE_APPROVAL decisions append `POLICY_DENIED` or
`APPROVAL_REQUIRED` through the caller's transaction boundary.

- [ ] **Step 3: Write approval/role/memory RED tests**

Tests cover approved, expired, consumed, revoked, wrong operation, wrong scope,
wrong target, wrong commit, and stale revision. Add separate tests proving a
developer cannot close QA or AppSec findings. Add strong secret fixtures:
PEM private-key headers and explicit credential assignment; ensure ordinary
SHA-256 digests are accepted.

Run: `go test ./internal/store -run 'Test(Approval|Finding|Memory)' -v`

Expected: compilation fails on missing record operations.

- [ ] **Step 4: Implement approval and ownership state machines**

Validate approval against stored immutable context and current UTC time.
Consume in the protected operation's transaction. Finding closure requires the
stored owner role; developer may only move a finding to
`ready_for_retest`. General memory rejects high-confidence secret material
without logging the value. Digest-shaped values remain valid engineering
evidence.

- [ ] **Step 5: Run GREEN and commit**

```bash
go test ./internal/policy ./internal/store -v
go test ./...
go vet ./...
git add internal/model internal/policy internal/store
git commit -m "feat(runtime): enforce local policy and approvals"
```

---

### Task 4: Real Git Worktree Isolation and Honest Bubblewrap Backend

**Files:**
- Create: `internal/model/isolation.go`
- Create: `internal/worktree/manager.go`
- Create: `internal/worktree/git.go`
- Create: `internal/sandbox/backend.go`
- Create: `internal/sandbox/bwrap_linux.go`
- Create: `internal/sandbox/bwrap_other.go`
- Test: `internal/worktree/manager_test.go`
- Test: `internal/sandbox/bwrap_linux_test.go`

**Interfaces:**
- Produces: `worktree.Manager.Prepare(ctx, model.WorktreeRequest) (model.Worktree, error)`
- Produces: `worktree.Manager.Inspect(ctx, path string) (model.WorktreeState, error)`
- Produces: `sandbox.Backend.Probe(ctx) model.IsolationCapability`
- Produces: `sandbox.Backend.Wrap(model.SandboxRequest, []string) (model.CommandSpec, error)`

- [ ] **Step 1: Write real-Git worktree RED tests**

Each test creates a temporary Git repository and an initial commit using the
real Git binary. Prove correct branch/base/path, idempotent prepare for the same
task, collision rejection, dirty-state blocking, and dirty worktree
preservation after cleanup request.

```go
func TestDirtyTaskWorktreeIsNeverDestroyed(t *testing.T) {
    repo := testgit.New(t)
    wt := prepareTaskWorktree(t, repo, "TASK-001")
    requireNoError(t, os.WriteFile(filepath.Join(wt.Path, "user.txt"), []byte("keep"), 0o600))

    err := worktree.New(repo.Path()).Remove(context.Background(), wt, false)
    if !errors.Is(err, model.ErrDirtyWorktree) {
        t.Fatalf("error=%v", err)
    }
    if _, err := os.Stat(filepath.Join(wt.Path, "user.txt")); err != nil {
        t.Fatalf("dirty data lost: %v", err)
    }
}
```

Run: `go test ./internal/worktree -v`

Expected: compilation fails because the manager is absent.

- [ ] **Step 2: Implement safe worktree manager**

Invoke Git with argv, never a shell. Resolve and compare canonical paths before
mutation. Use a deterministic `agent/<lower-task-id>-<short-random>` branch
recorded in SQLite before launch. Verify root, branch, HEAD, base, porcelain
status, and lease-bound path. Never call reset, clean, force, or recursive
filesystem deletion on unknown/dirty paths.

- [ ] **Step 3: Write bwrap capability RED tests**

Tests use a fake executable for argv construction and a real opt-in probe when
bwrap is installed. Assert writable worktree binding, isolated temp/home,
socket absence, `--unshare-net` for denied network, and explicit
`process_only`/blocked results when probe fails.

Run: `go test ./internal/sandbox -v`

Expected: compilation fails because backend types are absent.

- [ ] **Step 4: Implement bwrap without overclaiming**

Probe by executing a bounded `/usr/bin/true` inside the intended namespace
shape. Build an argv-only command that read-only binds required system
directories, writable-binds only the worktree and task log/artifact scratch,
creates tmpfs `/tmp` and an empty home, unshares user/pid/ipc/uts namespaces,
and unshares network only when denied. Do not mount `.slaves/runtime.sock`.

Return exact isolation dimensions. Network-denied Codex returns blocked before
launch. R2/R3 require successful bwrap probe; permitted R0/R1 may explicitly
return process-only. `SandboxRequest` accepts explicit read-only bind inputs;
the Codex caller supplies only its prepared minimal authentication view, never
the general user home or runtime socket.

- [ ] **Step 5: Run GREEN and commit**

```bash
go test ./internal/worktree ./internal/sandbox -v
go test ./...
go vet ./...
git add internal/model internal/worktree internal/sandbox
git commit -m "feat(runtime): add worktree worker isolation"
```

---

### Task 5: Durable Events, Artifacts, Worker Runs, and Verification Invalidation

**Files:**
- Create: `internal/model/evidence.go`
- Create: `internal/store/evidence.go`
- Create: `internal/artifact/store.go`
- Create: `internal/events/events.go`
- Test: `internal/store/evidence_test.go`
- Test: `internal/artifact/store_test.go`

**Interfaces:**
- Produces: `store.AppendEvent(ctx, tx, model.Event) error`
- Produces: `artifact.Store.Put(ctx, model.ArtifactInput) (model.Artifact, error)`
- Produces: `store.RecordRun(ctx, model.WorkerRun) error`
- Produces: `store.RecordVerification(ctx, model.Verification) error`
- Produces: `store.ObserveHEAD(ctx, taskID, newCommit string, expectedRevision int64) error`

- [ ] **Step 1: Write event/artifact RED tests**

Prove same event ID + same payload is idempotent, same ID + different payload
conflicts, artifact paths derive from computed bytes, supplied digest mismatch
rejects, and changed bytes produce changed identity.

Run: `go test ./internal/artifact ./internal/store -run 'Test(Event|Artifact)' -v`

Expected: compilation fails on missing evidence operations.

- [ ] **Step 2: Implement content-addressed artifacts and durable event rows**

Stream bytes through SHA-256 into a mode-`0600` temporary file within the
artifact directory, fsync, then atomically rename to
`sha256/<hex>`. Register metadata only after digest match. Event insertion
stores canonical JSON data and aggregate revision in the same caller-supplied
transaction as state.

- [ ] **Step 3: Write HEAD invalidation RED test**

```go
func TestHEADChangeInvalidatesVerificationAtomically(t *testing.T) {
    st := verifiedTaskStore(t, "TASK-001", commitA)
    requireNoError(t, st.ObserveHEAD(context.Background(), "TASK-001", commitB, currentRevision(t, st)))
    if verificationValid(t, st, "TASK-001", commitA) {
        t.Fatal("verification at commit A remained valid")
    }
    assertEventTypes(t, st, "HEAD_CHANGED", "VERIFICATION_INVALIDATED")
}
```

Run: `go test ./internal/store -run TestHEADChangeInvalidatesVerificationAtomically -v`

Expected: FAIL because HEAD observation is absent.

- [ ] **Step 4: Implement atomic HEAD observation**

If commit differs, compare task revision, update head/revision, invalidate every
currently valid verification for the old head, and append both required events
inside one transaction. No-op when the exact head is already recorded.

- [ ] **Step 5: Run GREEN and commit**

```bash
go test ./internal/artifact ./internal/store -v
go test ./...
git add internal/model internal/store internal/artifact internal/events
git commit -m "feat(runtime): add durable events and evidence"
```

---

### Task 6: Process-Managed Worker and Real Codex Adapter

**Files:**
- Create: `internal/adapter/adapter.go`
- Create: `internal/adapter/codex/codex.go`
- Create: `internal/adapter/codex/events.go`
- Create: `internal/testutil/fakecodex/fake.go`
- Create: `internal/worker/manager.go`
- Create: `internal/worker/process_linux.go`
- Create: `internal/worker/process_other.go`
- Test: `internal/adapter/codex/codex_test.go`
- Test: `internal/worker/manager_test.go`
- Test: `internal/integration/codex_real_test.go`

**Interfaces:**
- Produces: `adapter.Adapter{Probe, Run, Status, Resume, Capabilities, CollectEvidence, Shutdown}`
- Produces: `codex.New(binary string) adapter.Adapter`
- Produces: `worker.Manager.Run(ctx, model.RunRequest) (model.RunResult, error)`
- Produces: test-only `fakecodex.New(t testing.TB) (binary string, calls <-chan fakecodex.Call)`.

- [ ] **Step 1: Write Codex probe/envelope RED tests**

Use a deterministic fake `codex` binary that implements `--version`,
`exec --help`, and emits representative JSONL. Assert installed version,
exact safe flags, cwd, prompt on stdin, session ID extraction, normalized exit
status, and absence of dangerous bypass flags.

```go
func TestCodexRunUsesNarrowNativeSurface(t *testing.T) {
    fake, calls := fakecodex.New(t)
    a := codex.New(fake)
    _, err := a.Run(context.Background(), adapter.Request{Worktree: "/repo/task", Prompt: "structured"})
    requireNoError(t, err)
    got := <-calls
    want := []string{"exec", "--json", "-C", "/repo/task", "-s", "workspace-write", "-a", "never", "--ephemeral", "-"}
    if diff := cmpArgs(got.Args, want); diff != "" {
        t.Fatal(diff)
    }
}
```

Run: `go test ./internal/adapter/codex -v`

Expected: compilation fails because the adapter is absent.

- [ ] **Step 2: Implement the adapter contract**

Probe `codex --version` and `codex exec --help` with timeouts. Reject a CLI
whose help lacks required flags. Build a structured prompt from task ID, title,
base/head, allowed operations, worktree, required evidence, and explicit
no-self-approval instructions. Parse JSONL defensively, retain unknown events
as raw evidence, and never depend on experimental fields. Prepare a
mode-`0700` task-scoped Codex home containing only the authentication material
the installed CLI requires, mount it read-only through the sandbox request,
and do not copy general configuration, histories, sessions, or unrelated
credentials.

- [ ] **Step 3: Write worker crash/timeout RED tests**

Use real child processes that exit nonzero, ignore TERM, and create partial
logs. Assert the process group is terminated, session is failed/stale, task is
not complete/review, lease is released according to the explicit failure
transition, worktree remains, and WORKER_EXITED/log artifacts exist.

Run: `go test ./internal/worker -v`

Expected: compilation fails because the manager is absent.

- [ ] **Step 4: Implement worker lifecycle**

Persist REGISTER through EXIT transitions. Start the wrapped command with
`Setpgid`, monitor context/timeout, write heartbeats on a bounded ticker,
capture stdout/stderr without unbounded memory, terminate the process group
with TERM then KILL, and persist result/evidence before releasing resources.

Only exit zero + new commit + clean worktree transitions to `review`. Dirty,
no-commit, crash, nonzero, and timeout outcomes transition to explicit blocked
or failed states and never complete the task.

- [ ] **Step 5: Add opt-in real Codex integration**

Gate with `SLAVES_TEST_REAL_CODEX=1`. The test creates a temporary Git repo,
uses an R0 task prompt that requests one deterministic file and
commit, launches the installed authenticated Codex through the real adapter,
and asserts normalized evidence. Without the flag it reports SKIP, not PASS.

- [ ] **Step 6: Run GREEN and commit**

```bash
go test ./internal/adapter/... ./internal/worker -v
go test -race ./internal/worker ./internal/adapter/...
go test ./...
git add internal/adapter internal/worker internal/integration/codex_real_test.go
git commit -m "feat(runtime): add Codex execution worker"
```

---

### Task 7: Runtime Application, Unix-Socket API, Doctor, and CLI

**Files:**
- Create: `internal/app/runtime.go`
- Create: `internal/api/envelope.go`
- Create: `internal/api/server.go`
- Create: `internal/api/client.go`
- Create: `internal/doctor/doctor.go`
- Create: `internal/cli/cli.go`
- Modify: `cmd/slaves/main.go`
- Test: `internal/api/server_test.go`
- Test: `internal/doctor/doctor_test.go`
- Test: `internal/cli/cli_test.go`
- Test: `internal/integration/daemon_cli_test.go`

**Interfaces:**
- Produces: `app.Runtime` methods for status, agent registration, task import/list/show/claim/release, run, events, artifacts, verify, and reconciliation dry-run.
- Produces: `api.Server.Serve(ctx, socketPath string) error`
- Produces: `api.Client` matching application operations.
- Produces: `cli.Run(ctx, args, stdin, stdout, stderr) int`

- [ ] **Step 1: Write Unix-socket API RED tests**

Start a server against a temporary runtime directory. Assert socket mode
`0600`, no TCP address, version and health endpoints, request IDs, typed
conflict/policy/unavailable errors, malformed JSON rejection, body size limit,
and SIGTERM/context cleanup.

Run: `go test ./internal/api -v`

Expected: compilation fails because the API is absent.

- [ ] **Step 2: Implement app orchestration and local API**

Application methods compose policy before mutation, transactional store
operations, worktree/sandbox/worker flow, and evidence. Transport handlers only
decode, validate, call one application method, and encode the stable envelope.
Set header/read/write/idle timeouts even on the local socket. Refuse to replace
a live socket; remove only a positively identified stale socket.

- [ ] **Step 3: Write doctor RED tests**

Use controlled PATH/runtime fixtures to prove PASS/DEGRADED/FAIL for Git,
repository, pack/runtime version, SQLite integrity, permissions, socket,
worktree, Codex, bwrap, artifacts, and policy. Specifically assert missing
Codex is DEGRADED, corrupt SQLite is FAIL, invalid policy is FAIL, and missing
bwrap blocks R2/R3.

Run: `go test ./internal/doctor -v`

Expected: compilation fails because doctor is absent.

- [ ] **Step 4: Implement real probes**

Every probe records command/method, result, affected capability, and terse
detail without secret values. Probe bwrap by execution and Codex with bounded
version/help calls. Doctor never repairs destructive state.

- [ ] **Step 5: Write CLI RED tests**

Table-drive every required command and global `--json`. Assert exact
exit-code mapping and that mutating commands fail unavailable rather than
opening SQLite when daemon is absent.

```text
slaves init
slaves doctor
slaves status
slaves agent register --name local-codex --role developer
slaves agents
slaves tasks
slaves task import tasks.json [--dry-run]
slaves task show TASK-001
slaves task claim TASK-001 [--agent AGENT-...]
slaves task release TASK-001
slaves run TASK-001 --adapter codex [--agent AGENT-...]
slaves events
slaves artifacts
slaves verify [-- argv...]
slaves daemon
```

Run: `go test ./internal/cli -v`

Expected: compilation fails because parsing and rendering are absent.

- [ ] **Step 6: Implement CLI and daemon lifecycle**

Use `flag.FlagSet` per command, no Cobra. Human output remains concise; JSON
encodes domain results directly. `daemon` runs foreground, writes an
atomically-created PID file, handles SIGINT/SIGTERM, closes DB/server, and
removes its own socket/PID only.

- [ ] **Step 7: Write and pass end-to-end fake-adapter test**

Create a temporary Git repo, run init, start the real daemon, register a
developer, import `TASK-001`, claim/release, run using an injected
deterministic fake adapter, list events/artifacts, and verify JSON output and
resulting task state.

Run: `go test ./internal/integration -run TestDaemonCLIEndToEnd -v`

Expected first RED on missing route behavior, then PASS after the minimal route
and CLI implementation.

- [ ] **Step 8: Run GREEN and commit**

```bash
go test ./internal/app ./internal/api ./internal/doctor ./internal/cli ./internal/integration -v
go test ./...
go vet ./...
git add cmd/slaves internal/app internal/api internal/doctor internal/cli internal/integration
git commit -m "feat(cli): add executable local runtime commands"
```

---

### Task 8: Adversarial Runtime and Behavioral Conformance Coverage

**Files:**
- Create: `internal/integration/security_test.go`
- Create: `internal/integration/reconciliation_test.go`
- Create: conformance/runtime_runner.py (future executable scenario adapter)
- Modify: `conformance/behavioral_runner.py`
- Modify: `conformance/SCENARIOS.json`
- Test: `tools/tests_v6/test_runtime_runner.py`

**Interfaces:**
- Produces: executable scenario adapter mapping runtime test evidence to existing CONF IDs.
- Preserves: existing behavioral runner CLI and result envelope.

- [ ] **Step 1: Write adversarial integration RED tests**

Add named tests for:

- two agents cannot claim one task;
- developer cannot close QA/AppSec findings;
- wrong/expired/consumed/revoked approval rejection;
- stale task revision conflict;
- HEAD invalidates verification;
- secret-like general memory rejection;
- dirty worktree preservation;
- worker crash never completes;
- policy DENY prevents callback/process execution;
- network-denied sandbox never runs unrestricted;
- artifact digest mismatch rejection;
- policy-engine-down fail-closed;
- duplicate event idempotence/conflict;
- expired lease does not authorize stealing;
- runtime/file reconciliation conflict remains read-only.

Run: `go test ./internal/integration -run 'TestSecurity|TestReconciliation' -v`

Expected: at least one deliberate missing integration seam fails; implement
only the seam, not a new subsystem, then rerun to PASS.

- [ ] **Step 2: Write conformance-runner RED tests**

The Python test invokes a built `slaves` binary against temporary fixture
state and expects an evidence envelope containing scenario ID, executable
command, exit code, observed invariant, and current commit. It rejects a static
fixture-only PASS.

Run: `python -m unittest tools/tests_v6/test_runtime_runner.py -v`

Expected: FAIL because `runtime_runner.py` is absent.

- [ ] **Step 3: Implement executable scenario mapping**

Map CONF-003/004/005/006/008/009/010/018/019/023/024/025/026 to the exact Go
test or CLI integration that exercises the invariant. Keep unsupported
scenarios explicitly NOT_RUN. Do not change existing scenario meanings or
fabricate adapter execution.

- [ ] **Step 4: Run all behavioral/security tests and commit**

```bash
go test ./internal/integration -v
python -m unittest tools/tests_v6/test_runtime_runner.py -v
python -m unittest discover -s tools/tests_v6 -v
python conformance/runner.py validate-pack
git add internal/integration conformance tools/tests_v6/test_runtime_runner.py
git commit -m "test(runtime): add adversarial executable conformance"
```

---

### Task 9: Runtime Status Documentation and Version Truth

**Files:**
- Modify: `README.md`
- Modify: `runtime/README.md`
- Modify: `runtime/IMPLEMENTATION-ROADMAP.md`
- Modify: `RUNTIME-VERSION.yaml`
- Modify: `distribution/PACK-MANIFEST.json` only through the repository's existing manifest generator/verification workflow if required.
- Modify: `release/PACK-ATTESTATION.json` only through the existing unsigned-attestation workflow if required.
- Modify: `VERIFICATION.json` only through the existing verification metadata workflow if required.

**Interfaces:**
- Produces: accurate operator commands and implementation-status claims.
- Preserves: runtime spec 1.0.0, pack 6.0.0, and unsigned owner-attestation truth.

- [ ] **Step 1: Write the documentation truth checklist before editing**

Check exact runtime behavior with:

```bash
go run ./cmd/slaves --help
go run ./cmd/slaves --json doctor
go test ./...
```

Record which commands and isolation modes are actually available. Do not write
claims for skipped real Codex execution or future distributed features.

- [ ] **Step 2: Update only implementation status and operator usage**

Set:

```yaml
schema_version: 1
runtime_spec_version: "1.0.0"
status: local_runtime_milestone_1
implementation_claim: true
runtime_implementation_version: "0.1.0"
recommended_first_mode: local_daemon_sqlite
```

README text must distinguish implemented local Linux execution from
unimplemented distributed, multi-host, full MCP/A2A, production secret broker,
and non-Codex adapters. Document build, init, daemon, agent registration, task
import, run, verify, and JSON output without marketing inflation.

- [ ] **Step 3: Regenerate only repository-required integrity metadata**

Use the existing project tooling; do not hand-edit digests. Preserve
`UNSIGNED_BY_OWNER` and never claim signing.

Run:

```bash
python conformance/runner.py validate-pack
python tools/release_verify.py distribution/PACK-MANIFEST.json
```

If tool syntax differs, inspect `--help` and run its exact supported
equivalent. Do not change integrity metadata when validation proves it is not
required.

- [ ] **Step 4: Verify docs diff and commit**

```bash
git diff --check
python conformance/runner.py validate-pack
git add README.md runtime/README.md runtime/IMPLEMENTATION-ROADMAP.md RUNTIME-VERSION.yaml distribution/PACK-MANIFEST.json release/PACK-ATTESTATION.json VERIFICATION.json
git diff --cached --stat
git commit -m "docs(runtime): mark local execution milestone implemented"
```

Stage only files that actually changed; omit unchanged integrity paths from
`git add`.

---

### Task 10: Full Verification, Real Adapter Probe, and Feature-Branch Publication

**Files:**
- Modify only files required to fix failures caused by this branch.
- Do not change unrelated code to manufacture a green result.

**Interfaces:**
- Produces: fresh evidence for every final report field.

- [ ] **Step 1: Run formatting, build, unit, integration, vet, and race gates**

```bash
test -z "$(gofmt -l cmd internal)"
go test ./...
go vet ./...
go test -race ./...
go build ./cmd/slaves
```

Every command must exit 0. If race is unsupported by the environment, record
the exact tooling failure and do not claim race PASS.

- [ ] **Step 2: Run all existing repository verification**

```bash
python conformance/runner.py validate-pack
python -m unittest discover -s conformance/tests -v
python -m unittest discover -s tools/tests -v
python -m unittest discover -s tools/tests_v6 -v
python tools/agentos.py pack-status
```

Expected baseline: 6 conformance tests, 3 tools tests, and at least the original
9 V6 tools tests plus the new runtime-runner tests, all with zero failures.

- [ ] **Step 3: Run runtime smoke evidence**

In a temporary Git repository:

```text
slaves init
slaves daemon
slaves agent register --name smoke-codex --role developer
slaves task import tasks.json
slaves status
slaves tasks
slaves task show TASK-001
slaves task claim TASK-001
slaves task release TASK-001
slaves events
slaves artifacts
slaves verify
```

Capture exit codes and structured results without committing `.slaves/`.

- [ ] **Step 4: Probe and optionally execute real Codex**

Always run:

```bash
codex --version
codex exec --help
```

Run `SLAVES_TEST_REAL_CODEX=1 go test ./internal/integration -run
TestRealCodex -v` because this task explicitly requires real adapter
verification and the installed CLI is authenticated. If external service,
quota, or sandbox policy prevents execution, report `Real adapter execution
tested: NO` with the exact failure; do not weaken sandbox flags.

- [ ] **Step 5: Review security and Git state**

```bash
git status --short
git diff --check
git diff main...HEAD --stat
git diff main...HEAD
git log --oneline --decorate main..HEAD
rg -n --hidden --glob '!.git/**' --glob '!.slaves/**' +  '(BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY|api[_-]?key[[:space:]]*[:=]|password[[:space:]]*[:=]|token[[:space:]]*[:=])' .
```

Inspect every match without printing secret values. Confirm `.slaves/` is
ignored and no database, socket, log, artifact bytes, auth file, or private key
is tracked.

- [ ] **Step 6: Commit only a necessary final fix, then push feature branch**

If Step 1–5 required a branch-caused fix, repeat its targeted Red/Green test and
commit one accurate logical fix. Otherwise create no empty commit.

```bash
git status
git push -u origin feat/local-runtime-v1
```

Never force-push and never merge to `main`. After push, compare local HEAD to
`refs/remotes/origin/feat/local-runtime-v1` and prepare the exact requested
final report with unsupported/unverified items named explicitly.

---

## Plan Self-Review

- **Spec coverage:** All acceptance areas map to Tasks 1–10: executable entry,
  init/doctor/daemon/API/CLI, schema/migrations, claims/sessions, policy and
  approvals, worktree/bwrap, Codex, worker failures, events/artifacts,
  verification invalidation, reconciliation, security, conformance, docs,
  versioning, commits, and push.
- **Scope:** SQLite/local Linux/Codex only. No distributed infrastructure,
  remote MCP/A2A server, production secret broker, or speculative adapter.
- **Type consistency:** Store, policy, worktree, sandbox, adapter, worker,
  application, API, and CLI interfaces have one owner and downstream callers.
- **Placeholder scan:** The plan contains no deferred implementation
  placeholders; optional real Codex execution has an explicit evidence outcome,
  not a fabricated PASS.
