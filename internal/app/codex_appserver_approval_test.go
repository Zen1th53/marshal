package app

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/codex"
	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
)

type fakeLiveCodexAppServer struct {
	resolved    int
	declined    int
	interrupted int
}

func (f *fakeLiveCodexAppServer) StartSession(context.Context, adapter.Request) (codex.AppServerSession, error) {
	return codex.AppServerSession{}, fmt.Errorf("unexpected start")
}
func (f *fakeLiveCodexAppServer) ResumeSession(context.Context, string, adapter.Request) (codex.AppServerSession, error) {
	return codex.AppServerSession{}, fmt.Errorf("unexpected resume")
}
func (f *fakeLiveCodexAppServer) StartTurn(context.Context, codex.AppServerSession, adapter.Request) (codex.AppServerTurn, error) {
	return codex.AppServerTurn{}, fmt.Errorf("unexpected turn start")
}
func (f *fakeLiveCodexAppServer) WaitTurn(context.Context, codex.AppServerTurn) (codex.AppServerTurnResult, error) {
	return codex.AppServerTurnResult{}, fmt.Errorf("unexpected wait")
}
func (f *fakeLiveCodexAppServer) InterruptTurn(context.Context, codex.AppServerTurn) error {
	f.interrupted++
	return nil
}
func (f *fakeLiveCodexAppServer) DeclineApproval(context.Context, codex.AppServerApprovalRequest) error {
	f.declined++
	return nil
}
func (f *fakeLiveCodexAppServer) ResolveApproval(ctx context.Context, request codex.AppServerApprovalRequest, authority codex.AppServerApprovalAuthority) error {
	if err := authority.ApproveAppServerRequest(ctx, request); err != nil {
		return err
	}
	f.resolved++
	return nil
}
func (f *fakeLiveCodexAppServer) Close() error { return nil }

type completingCodexAppServer struct{ sessions, turns, waits, closes int }

func (f *completingCodexAppServer) StartSession(_ context.Context, request adapter.Request) (codex.AppServerSession, error) {
	f.sessions++
	return codex.AppServerSession{ThreadID: "thread-complete", SessionID: "session-complete", Model: request.Model, Worktree: request.Worktree, Persistent: true}, nil
}
func (f *completingCodexAppServer) ResumeSession(context.Context, string, adapter.Request) (codex.AppServerSession, error) {
	return codex.AppServerSession{}, fmt.Errorf("unexpected resume")
}
func (f *completingCodexAppServer) StartTurn(_ context.Context, session codex.AppServerSession, _ adapter.Request) (codex.AppServerTurn, error) {
	f.turns++
	return codex.AppServerTurn{ThreadID: session.ThreadID, TurnID: "turn-complete"}, nil
}
func (f *completingCodexAppServer) WaitTurn(_ context.Context, turn codex.AppServerTurn) (codex.AppServerTurnResult, error) {
	f.waits++
	return codex.AppServerTurnResult{ThreadID: turn.ThreadID, TurnID: turn.TurnID, Status: "completed", FinalText: "completed safely"}, nil
}
func (f *completingCodexAppServer) InterruptTurn(context.Context, codex.AppServerTurn) error {
	return nil
}
func (f *completingCodexAppServer) DeclineApproval(context.Context, codex.AppServerApprovalRequest) error {
	return nil
}
func (f *completingCodexAppServer) ResolveApproval(context.Context, codex.AppServerApprovalRequest, codex.AppServerApprovalAuthority) error {
	return fmt.Errorf("unexpected approval")
}
func (f *completingCodexAppServer) Close() error { f.closes++; return nil }

type blockingCodexAppServer struct {
	completingCodexAppServer
	waitStarted chan struct{}
	interrupted int
}

func (f *blockingCodexAppServer) WaitTurn(ctx context.Context, turn codex.AppServerTurn) (codex.AppServerTurnResult, error) {
	f.waits++
	select {
	case f.waitStarted <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return codex.AppServerTurnResult{ThreadID: turn.ThreadID, TurnID: turn.TurnID}, ctx.Err()
}

func (f *blockingCodexAppServer) InterruptTurn(context.Context, codex.AppServerTurn) error {
	f.interrupted++
	return nil
}

func testNativeCodexApproval() codex.AppServerApprovalRequest {
	return codex.AppServerApprovalRequest{
		RequestID: "request-1", Method: "item/commandExecution/requestApproval",
		ThreadID: "thread-1", TurnID: "turn-1", ItemID: "item-1", ApprovalID: "native-1",
		Kind: "command", Command: "secret-looking-command-must-not-persist", Worktree: "/worktree", Digest: "sha256:native-request",
	}
}

// attachRunningNativeTestRun supplies the same durable Process 05 position
// that executeProcess05CodexAppServer receives from Engine.ExecuteRun in
// production. Direct adapter tests must not bypass that ownership merely to
// avoid constructing the canonical run.
func attachRunningNativeTestRun(t *testing.T, runtime *Runtime, task execution.TaskExecution, projectRoot string) *execution.Engine {
	t.Helper()
	engine, err := execution.NewEngine(execution.EngineConfig{ProjectRoot: projectRoot}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if task.State == "" {
		task.State = execution.TaskRunning
	}
	if task.WorktreePath == "" {
		task.WorktreePath = projectRoot
	}
	run := execution.ExecutionRun{RunID: task.RunID, Version: task.RunRevision, State: execution.RunRunning, Tasks: map[string]execution.TaskExecution{task.TaskID: task}}
	if err := engine.RunStore().CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	runtime.execService = &ExecutionService{runtime: runtime, engine: engine, now: time.Now}
	return engine
}

func TestCodexAppServerApprovalBridgeIsDurableExactAndOneShot(t *testing.T) {
	now := time.Date(2026, 9, 13, 1, 2, 3, 0, time.UTC)
	bridge := &CodexAppServerApprovalBridge{
		manager: execution.NewApprovalManager(t.TempDir()),
		run:     execution.ExecutionRun{RunID: "run-1", PlanID: "plan-1", PlanVersion: 7},
		taskID:  "task-1", now: func() time.Time { return now },
	}
	request := testNativeCodexApproval()
	created, err := bridge.Request(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if created.OperationType != codexAppServerApprovalOperation || created.Scope != codexAppServerApprovalScope(request) {
		t.Fatalf("native approval lost exact binding: %#v", created)
	}
	if created.DiffPreview != request.Method {
		t.Fatalf("native operation type not recorded: %#v", created)
	}
	// Raw shell-like content must not be put into the durable approval.
	if created.DiffPreview == request.Command || created.Scope == request.Command {
		t.Fatalf("raw native command leaked into durable approval: %#v", created)
	}
	again, err := bridge.Request(context.Background(), request)
	if err != nil || again.ApprovalID != created.ApprovalID {
		t.Fatalf("duplicate native request must converge: again=%#v err=%v", again, err)
	}
	if err := bridge.manager.Approve(created.ApprovalID, "operator", "reviewed", now); err != nil {
		t.Fatal(err)
	}
	if err := bridge.ApproveAppServerRequest(context.Background(), request); err != nil {
		t.Fatalf("consume exact approved native request: %v", err)
	}
	if err := bridge.ApproveAppServerRequest(context.Background(), request); !errors.Is(err, execution.ErrApprovalRequired) && !errors.Is(err, execution.ErrInvalidStateTransition) {
		t.Fatalf("replayed native approval must fail: %v", err)
	}
}

func TestCodexAppServerApprovalBridgeRejectsChangedNativeBinding(t *testing.T) {
	now := time.Now().UTC()
	bridge := &CodexAppServerApprovalBridge{
		manager: execution.NewApprovalManager(),
		run:     execution.ExecutionRun{RunID: "run-1", PlanID: "plan-1", PlanVersion: 7},
		taskID:  "task-1", now: func() time.Time { return now },
	}
	request := testNativeCodexApproval()
	created, err := bridge.Request(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := bridge.manager.Approve(created.ApprovalID, "operator", "reviewed", now); err != nil {
		t.Fatal(err)
	}
	changed := request
	changed.Digest = "sha256:changed"
	if err := bridge.ApproveAppServerRequest(context.Background(), changed); !errors.Is(err, execution.ErrApprovalRequired) {
		t.Fatalf("changed native request = %v, want approval required", err)
	}
	if current, err := bridge.manager.GetApproval(created.ApprovalID); err != nil || current.Status != execution.ApprovalApproved {
		t.Fatalf("changed request must not consume original approval: current=%#v err=%v", current, err)
	}
}

func TestNativeCodexApprovalCannotBeDecidedAsOrdinaryTaskApproval(t *testing.T) {
	engine, err := execution.NewEngine(execution.EngineConfig{ProjectRoot: t.TempDir()}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	record, err := engine.ApprovalManager().RequestApproval(execution.ApprovalRequest{
		RunID: "run-native", TaskID: "task-native", OperationType: codexAppServerApprovalOperation,
		TargetResource: "worktree", Scope: "native", CurrentState: "bound", Parameters: "sha256:one",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.DecideApproval(context.Background(), record.ApprovalID, true, "operator", "approve"); !errors.Is(err, execution.ErrRunBlocked) {
		t.Fatalf("ordinary decision must not release native provider turn: %v", err)
	}
	current, err := engine.ApprovalManager().GetApproval(record.ApprovalID)
	if err != nil || current.Status != execution.ApprovalRequested {
		t.Fatalf("blocked ordinary decision mutated native approval: %#v, %v", current, err)
	}
}

func TestExecutionServiceNativeCodexApprovalConsumesExactLiveTurn(t *testing.T) {
	ctx := context.Background()
	engine, err := execution.NewEngine(execution.EngineConfig{ProjectRoot: t.TempDir()}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := testNativeCodexApproval()
	now := time.Now().UTC()
	// Seed a canonical run/task in the exact paused state a Process 05 native
	// worker leaves behind. The record is created by the same digest-bound
	// bridge used in production; no TUI-local approval is fabricated.
	run := execution.ExecutionRun{RunID: "run-live", Version: 1, PlanID: "plan-live", PlanVersion: 1, State: execution.RunNeedsApproval, Tasks: map[string]execution.TaskExecution{
		"task-live": {RunID: "run-live", TaskID: "task-live", State: execution.TaskNeedsApproval, NativeTurn: &execution.NativeTurnBinding{Provider: "codex-app-server", ThreadID: request.ThreadID, TurnID: request.TurnID, Worktree: request.Worktree, WorktreeDigest: "sha256:worktree", ConstraintDigest: "sha256:constraints", RunRevision: 1, State: "APPROVAL_REQUIRED"}},
	}}
	if err := engine.RunStore().CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	serviceRuntime := &Runtime{codexAppServerTurns: make(map[string]*liveCodexAppServerTurn)}
	service := &ExecutionService{runtime: serviceRuntime, engine: engine, now: func() time.Time { return now }}
	bridge, err := service.CodexAppServerApprovals(ctx, run.RunID, "task-live")
	if err != nil {
		t.Fatal(err)
	}
	record, err := bridge.Request(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	// The task record stores the canonical queue ID before the UI sees it.
	current, err := engine.GetRun(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	task := current.Tasks["task-live"]
	task.ApprovalID = record.ApprovalID
	task.ApprovalRequired = true
	// UpdateRun below advances the canonical version to 2; this is the
	// equivalent persisted post-pause binding Engine writes for a live turn.
	task.NativeTurn.RunRevision = 2
	current.Tasks[task.TaskID] = task
	if err := engine.RunStore().UpdateRun(ctx, current); err != nil {
		t.Fatal(err)
	}
	live := &fakeLiveCodexAppServer{}
	serviceRuntime.codexAppServerTurns[run.RunID+"\x00"+task.TaskID] = &liveCodexAppServerTurn{client: live, binding: *task.NativeTurn, approval: &request}

	if err := service.Approve(ctx, record.ApprovalID, "operator", "reviewed exact request"); err != nil {
		t.Fatalf("approve live native request: %v", err)
	}
	if live.resolved != 1 || live.declined != 0 {
		t.Fatalf("native decision = resolved:%d declined:%d, want 1/0", live.resolved, live.declined)
	}
	consumed, err := engine.ApprovalManager().GetApproval(record.ApprovalID)
	if err != nil || consumed.Status != execution.ApprovalConsumed {
		t.Fatalf("native approval must be one-shot consumed: %#v %v", consumed, err)
	}
	// Duplicate UI submission cannot cause a second native accept.
	if err := service.Approve(ctx, record.ApprovalID, "operator", "replay"); err == nil {
		t.Fatal("duplicate native approval was accepted")
	}
	if live.resolved != 1 {
		t.Fatalf("duplicate approval called native accept %d times", live.resolved)
	}
}

func TestProcess05CodexAppServerStartsOneTypedTurnAndRefusesReplacement(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	server := &completingCodexAppServer{}
	runtime := &Runtime{evidenceSanitizer: evidence.NewStrictSanitizer(evidence.SanitizerConfig{}), codexAppServerTurns: make(map[string]*liveCodexAppServerTurn), codexAppServerNew: func(string, string) codexAppServerClient { return server }}
	pkg := execution.ConstraintPackage{TaskID: "task-live", TaskDescription: "perform bounded change", HardConstraints: []string{"do not leak secrets"}}
	pkg.Digest = execution.ComputeConstraintDigest(pkg)
	task := execution.TaskExecution{RunID: "run-live", RunRevision: 1, TaskID: "task-live", AssignedModel: "gpt-5.6-terra", State: execution.TaskRunning, WorktreePath: repo.Path()}
	attachRunningNativeTestRun(t, runtime, task, repo.Path())
	client := codex.NewWithModels("/usr/bin/codex", nil, []codex.ModelInfo{{Slug: "gpt-5.6-terra"}}, "gpt-5.6-terra")
	request := adapter.Request{TaskID: "TASK-P05-LIVE", Title: pkg.TaskDescription, Worktree: repo.Path(), Model: task.AssignedModel, TrustedContext: pkg.FormatPromptHeader()}
	result, handled, err := runtime.executeProcess05CodexAppServer(ctx, task, pkg, repo.Path(), client, request, "codex-test")
	if err != nil || !handled || !result.Success || result.NativeTurn == nil {
		t.Fatalf("app-server typed execution = result:%#v handled:%v err:%v", result, handled, err)
	}
	if server.sessions != 1 || server.turns != 1 || server.waits != 1 {
		t.Fatalf("typed app-server lifecycle counts = sessions:%d turns:%d waits:%d", server.sessions, server.turns, server.waits)
	}
	// A durable completed binding cannot be used to start another turn after
	// process-memory state has gone away; recovery must be explicit.
	task.NativeTurn = result.NativeTurn
	if _, handled, err := runtime.executeProcess05CodexAppServer(ctx, task, pkg, repo.Path(), client, request, "codex-test"); !handled || !errors.Is(err, model.ErrUnavailable) {
		t.Fatalf("replacement turn after lost client = handled:%v err:%v", handled, err)
	}
	if server.turns != 1 {
		t.Fatalf("recovery attempted duplicate turn/start: %d", server.turns)
	}
}

// A failure after thread/turn creation but before the durable binding is
// saved must close the native process and cancel its context. Otherwise a
// failed worktree read leaves a live provider turn that no canonical run owns.
func TestProcess05CodexAppServerCleansUpIfInitialWorktreeDigestFails(t *testing.T) {
	ctx := context.Background()
	server := &completingCodexAppServer{}
	runtime := &Runtime{evidenceSanitizer: evidence.NewStrictSanitizer(evidence.SanitizerConfig{}), codexAppServerTurns: make(map[string]*liveCodexAppServerTurn), codexAppServerNew: func(string, string) codexAppServerClient { return server }}
	pkg := execution.ConstraintPackage{TaskID: "task-digest-failure", TaskDescription: "perform bounded change"}
	pkg.Digest = execution.ComputeConstraintDigest(pkg)
	task := execution.TaskExecution{RunID: "run-digest-failure", RunRevision: 1, TaskID: "task-digest-failure", AssignedModel: "gpt-5.6-terra"}
	client := codex.NewWithModels("/usr/bin/codex", nil, []codex.ModelInfo{{Slug: "gpt-5.6-terra"}}, "gpt-5.6-terra")
	request := adapter.Request{TaskID: "TASK-P05-DIGEST-FAILURE", Title: pkg.TaskDescription, Worktree: "/path/that/does/not/exist", Model: task.AssignedModel, TrustedContext: pkg.FormatPromptHeader()}
	if _, handled, err := runtime.executeProcess05CodexAppServer(ctx, task, pkg, request.Worktree, client, request, "codex-test"); !handled || err == nil {
		t.Fatalf("initial digest failure = handled:%v err:%v", handled, err)
	}
	if server.sessions != 1 || server.turns != 1 || server.closes != 1 {
		t.Fatalf("failed native setup was not cleaned up: sessions=%d turns=%d closes=%d", server.sessions, server.turns, server.closes)
	}
	if len(runtime.codexAppServerTurns) != 0 {
		t.Fatalf("failed native setup retained live binding: %#v", runtime.codexAppServerTurns)
	}
}

func TestProcess05CodexAppServerRejectsTerminalSuccessWithoutMutatingWorktree(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	server := &completingCodexAppServer{}
	runtime := &Runtime{evidenceSanitizer: evidence.NewStrictSanitizer(evidence.SanitizerConfig{}), codexAppServerTurns: make(map[string]*liveCodexAppServerTurn), codexAppServerNew: func(string, string) codexAppServerClient { return server }}
	pkg := execution.ConstraintPackage{TaskID: "task-no-change", TaskDescription: "perform bounded change"}
	pkg.Digest = execution.ComputeConstraintDigest(pkg)
	task := execution.TaskExecution{RunID: "run-no-change", RunRevision: 1, TaskID: "task-no-change", AssignedModel: "gpt-5.6-terra", Mutates: true, State: execution.TaskRunning, WorktreePath: repo.Path()}
	attachRunningNativeTestRun(t, runtime, task, repo.Path())
	client := codex.NewWithModels("/usr/bin/codex", nil, []codex.ModelInfo{{Slug: "gpt-5.6-terra"}}, "gpt-5.6-terra")
	request := adapter.Request{TaskID: "TASK-P05-NO-CHANGE", Title: pkg.TaskDescription, Worktree: repo.Path(), Model: task.AssignedModel, TrustedContext: pkg.FormatPromptHeader()}
	if _, handled, err := runtime.executeProcess05CodexAppServer(ctx, task, pkg, repo.Path(), client, request, "codex-test"); !handled || !errors.Is(err, model.ErrConflict) {
		t.Fatalf("terminal no-change mutation = handled:%v err:%v", handled, err)
	}
}

func TestProcess05CodexAppServerSanitizesTerminalOutputBeforeCancellingResumedTurn(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	server := &completingCodexAppServer{}
	runtime := &Runtime{evidenceSanitizer: evidence.NewStrictSanitizer(evidence.SanitizerConfig{}), codexAppServerTurns: make(map[string]*liveCodexAppServerTurn)}
	pkg := execution.ConstraintPackage{TaskID: "task-resume", TaskDescription: "perform bounded change", HardConstraints: []string{"do not leak secrets"}}
	pkg.Digest = execution.ComputeConstraintDigest(pkg)
	digest, err := execution.WorkspaceTreeDigest(repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	turnCtx, turnCancel := context.WithCancel(ctx)
	defer turnCancel()
	binding := execution.NativeTurnBinding{Provider: "codex-app-server", ThreadID: "thread-complete", TurnID: "turn-complete", SessionID: "thread-complete", Worktree: repo.Path(), WorktreeDigest: digest, Model: "gpt-5.6-terra", ConstraintDigest: pkg.Digest, RunRevision: 1, State: "RUNNING"}
	task := execution.TaskExecution{RunID: "run-resume", RunRevision: 1, TaskID: "task-resume", AssignedModel: "gpt-5.6-terra", NativeTurn: &binding}
	runtime.codexAppServerTurns[task.RunID+"\x00"+task.TaskID] = &liveCodexAppServerTurn{client: server, binding: binding, runCtx: turnCtx, cancel: turnCancel, accepted: true}
	client := codex.NewWithModels("/usr/bin/codex", nil, []codex.ModelInfo{{Slug: "gpt-5.6-terra"}}, "gpt-5.6-terra")
	request := adapter.Request{TaskID: "TASK-P05-RESUME", Title: pkg.TaskDescription, Worktree: repo.Path(), Model: task.AssignedModel, TrustedContext: pkg.FormatPromptHeader()}
	result, handled, err := runtime.executeProcess05CodexAppServer(turnCtx, task, pkg, repo.Path(), client, request, "codex-test")
	if err != nil || !handled || !result.Success {
		t.Fatalf("terminal resumed turn = result:%#v handled:%v err:%v", result, handled, err)
	}
	if turnCtx.Err() == nil {
		t.Fatal("terminal native turn was not cleaned up")
	}
}

func TestExecutionServiceCancelsOnlyAfterTypedNativeInterrupt(t *testing.T) {
	ctx := context.Background()
	engine, err := execution.NewEngine(execution.EngineConfig{ProjectRoot: t.TempDir()}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	worktree := t.TempDir()
	run := execution.ExecutionRun{RunID: "run-cancel", Version: 1, State: execution.RunRunning, Tasks: map[string]execution.TaskExecution{
		"task-cancel": {RunID: "run-cancel", TaskID: "task-cancel", State: execution.TaskRunning, NativeTurn: &execution.NativeTurnBinding{Provider: "codex-app-server", ThreadID: "thread-cancel", TurnID: "turn-cancel", Worktree: worktree, WorktreeDigest: "sha256:worktree", ConstraintDigest: "sha256:constraints", RunRevision: 1, State: "RUNNING"}},
	}}
	if err := engine.RunStore().CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	live := &fakeLiveCodexAppServer{}
	runtime := &Runtime{codexAppServerTurns: map[string]*liveCodexAppServerTurn{"run-cancel\x00task-cancel": {client: live, binding: *run.Tasks["task-cancel"].NativeTurn}}}
	service := &ExecutionService{runtime: runtime, engine: engine, now: time.Now}
	if err := service.CancelCodexAppServerTurn(ctx, "run-cancel", "task-cancel", "operator cancelled"); err != nil {
		t.Fatal(err)
	}
	if live.interrupted != 1 {
		t.Fatalf("typed turn/interrupt calls = %d, want 1", live.interrupted)
	}
	current, err := engine.GetRun(ctx, "run-cancel")
	if err != nil || current.State != execution.RunCancelled || current.Tasks["task-cancel"].State != execution.TaskCancelled {
		t.Fatalf("cancellation was not durable: %#v %v", current, err)
	}
	if err := service.CancelCodexAppServerTurn(ctx, "run-cancel", "task-cancel", "replay"); !errors.Is(err, model.ErrUnavailable) {
		t.Fatalf("duplicate cancellation must not send another interrupt: %v", err)
	}
	if live.interrupted != 1 {
		t.Fatalf("duplicate cancellation interrupted %d times", live.interrupted)
	}
}

// A turn must become durable before the provider wait begins. This covers the
// cancellation race where an operator presses Control/Cancel while Codex is
// still producing a response: the typed interrupt is followed by a durable
// Process 05 cancellation, never an in-memory-only stop.
func TestProcess05CodexAppServerBindsLiveTurnBeforeCancellation(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	server := &blockingCodexAppServer{waitStarted: make(chan struct{}, 1)}
	runtime := &Runtime{evidenceSanitizer: evidence.NewStrictSanitizer(evidence.SanitizerConfig{}), codexAppServerTurns: make(map[string]*liveCodexAppServerTurn), codexAppServerNew: func(string, string) codexAppServerClient { return server }}
	pkg := execution.ConstraintPackage{TaskID: "task-live-cancel", TaskDescription: "perform bounded change"}
	pkg.Digest = execution.ComputeConstraintDigest(pkg)
	task := execution.TaskExecution{RunID: "run-live-cancel", RunRevision: 1, TaskID: "task-live-cancel", AssignedModel: "gpt-5.6-terra", State: execution.TaskRunning, WorktreePath: repo.Path()}
	engine := attachRunningNativeTestRun(t, runtime, task, repo.Path())
	client := codex.NewWithModels("/usr/bin/codex", nil, []codex.ModelInfo{{Slug: "gpt-5.6-terra"}}, "gpt-5.6-terra")
	request := adapter.Request{TaskID: "TASK-P05-LIVE-CANCEL", Title: pkg.TaskDescription, Worktree: repo.Path(), Model: task.AssignedModel, TrustedContext: pkg.FormatPromptHeader()}
	type turnOutcome struct {
		handled bool
		err     error
	}
	done := make(chan turnOutcome, 1)
	go func() {
		_, handled, err := runtime.executeProcess05CodexAppServer(ctx, task, pkg, repo.Path(), client, request, "codex-test")
		done <- turnOutcome{handled: handled, err: err}
	}()
	select {
	case <-server.waitStarted:
	case <-time.After(time.Second):
		t.Fatal("native provider never entered WaitTurn")
	}
	bound, err := engine.GetRun(ctx, task.RunID)
	if err != nil || bound.Tasks[task.TaskID].NativeTurn == nil {
		t.Fatalf("live turn was not durably bound before cancellation: run=%#v err=%v", bound, err)
	}
	if err := runtime.Execution().CancelCodexAppServerTurn(ctx, task.RunID, task.TaskID, "operator cancelled"); err != nil {
		t.Fatalf("typed native cancellation: %v", err)
	}
	select {
	case result := <-done:
		if !result.handled || result.err == nil {
			t.Fatalf("cancelled wait result = %+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled native wait did not terminate")
	}
	cancelled, err := engine.GetRun(ctx, task.RunID)
	if err != nil || cancelled.State != execution.RunCancelled || cancelled.Tasks[task.TaskID].State != execution.TaskCancelled {
		t.Fatalf("native cancellation was not durable: run=%#v err=%v", cancelled, err)
	}
	if server.interrupted != 1 {
		t.Fatalf("typed interrupt calls = %d, want 1", server.interrupted)
	}
}
