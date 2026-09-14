package app

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/claude"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
)

// fakeLiveClaudeStream is the hermetic stand-in for a live local Claude stream
// process. It records exactly how many times each typed lifecycle call was
// made, and observes the canonical approval status at the instant the provider
// is told, so ordering (record decided BEFORE the provider is released) can be
// asserted rather than assumed.
type fakeLiveClaudeStream struct {
	resolved    int
	declined    int
	interrupted int
	// resolveErr, when set, is returned by ResolveApproval before the
	// authority is consulted, standing in for a dead local process.
	resolveErr error
	// statusAtResolve captures the canonical record status observed from
	// inside ResolveApproval.
	statusAtResolve execution.ApprovalStatus
	observe         func() execution.ApprovalStatus
}

func (f *fakeLiveClaudeStream) StartSession(context.Context, adapter.Request) (claude.StreamSession, error) {
	return claude.StreamSession{}, fmt.Errorf("unexpected start")
}
func (f *fakeLiveClaudeStream) ResumeSession(context.Context, string, adapter.Request) (claude.StreamSession, error) {
	return claude.StreamSession{}, fmt.Errorf("unexpected resume")
}
func (f *fakeLiveClaudeStream) StartTurn(context.Context, claude.StreamSession, adapter.Request) (claude.StreamTurn, error) {
	return claude.StreamTurn{}, fmt.Errorf("unexpected turn start")
}
func (f *fakeLiveClaudeStream) WaitTurn(context.Context, claude.StreamTurn) (claude.StreamTurnResult, error) {
	return claude.StreamTurnResult{}, fmt.Errorf("unexpected wait")
}
func (f *fakeLiveClaudeStream) InterruptTurn(context.Context, claude.StreamTurn) error {
	f.interrupted++
	return nil
}
func (f *fakeLiveClaudeStream) DeclineApproval(context.Context, claude.StreamApprovalRequest) error {
	f.declined++
	return nil
}
func (f *fakeLiveClaudeStream) ResolveApproval(ctx context.Context, request claude.StreamApprovalRequest, authority claude.StreamApprovalAuthority) error {
	if f.observe != nil {
		f.statusAtResolve = f.observe()
	}
	if f.resolveErr != nil {
		return f.resolveErr
	}
	if err := authority.ApproveStreamRequest(ctx, request); err != nil {
		return err
	}
	f.resolved++
	return nil
}
func (f *fakeLiveClaudeStream) Close() error { return nil }

func testNativeClaudeApproval(worktree string) claude.StreamApprovalRequest {
	return claude.StreamApprovalRequest{
		RequestID: "claude-request-1", Method: "tool/bash/requestPermission",
		SessionID: "session-1", TurnID: "turn-1", ToolName: "Bash",
		Command: "secret-looking-command-must-not-persist", Worktree: worktree,
		Digest: "sha256:native-claude-request",
	}
}

// claudeStreamApprovalFixture seeds the exact durable Process 05 position a
// native Claude worker leaves behind when it pauses on a permission request:
// a run in NEEDS_APPROVAL, a task bound to a persisted native turn, and a
// canonical approval record created by the same digest-bound bridge used in
// production. No TUI-local approval is fabricated.
type claudeStreamApprovalFixture struct {
	service *ExecutionService
	runtime *Runtime
	engine  *execution.Engine
	record  *execution.RuntimeApproval
	request claude.StreamApprovalRequest
	task    execution.TaskExecution
	key     string
}

func newClaudeStreamApprovalFixture(t *testing.T) *claudeStreamApprovalFixture {
	t.Helper()
	ctx := context.Background()
	engine, err := execution.NewEngine(execution.EngineConfig{ProjectRoot: t.TempDir()}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	worktree := t.TempDir()
	request := testNativeClaudeApproval(worktree)
	now := time.Now().UTC()
	run := execution.ExecutionRun{RunID: "run-claude-live", Version: 1, PlanID: "plan-claude-live", PlanVersion: 1, State: execution.RunNeedsApproval, Tasks: map[string]execution.TaskExecution{
		"task-claude-live": {RunID: "run-claude-live", TaskID: "task-claude-live", State: execution.TaskNeedsApproval, NativeTurn: &execution.NativeTurnBinding{Provider: "claude-stream", ThreadID: request.SessionID, SessionID: request.SessionID, TurnID: request.TurnID, Worktree: worktree, WorktreeDigest: "sha256:worktree", ConstraintDigest: "sha256:constraints", RunRevision: 1, State: "APPROVAL_REQUIRED"}},
	}}
	if err := engine.RunStore().CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	serviceRuntime := &Runtime{claudeStreamTurns: make(map[string]*liveClaudeStreamTurn)}
	service := &ExecutionService{runtime: serviceRuntime, engine: engine, now: func() time.Time { return now }}
	serviceRuntime.execService = service
	bridge, err := service.ClaudeStreamApprovals(ctx, run.RunID, "task-claude-live")
	if err != nil {
		t.Fatal(err)
	}
	record, err := bridge.Request(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	current, err := engine.GetRun(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	task := current.Tasks["task-claude-live"]
	task.ApprovalID = record.ApprovalID
	task.ApprovalRequired = true
	// UpdateRun below advances the canonical version to 2; this is the
	// equivalent persisted post-pause binding Engine writes for a live turn.
	task.NativeTurn.RunRevision = 2
	current.Tasks[task.TaskID] = task
	if err := engine.RunStore().UpdateRun(ctx, current); err != nil {
		t.Fatal(err)
	}
	return &claudeStreamApprovalFixture{
		service: service, runtime: serviceRuntime, engine: engine,
		record: record, request: request, task: task,
		key: run.RunID + "\x00" + task.TaskID,
	}
}

func (f *claudeStreamApprovalFixture) bindLiveTurn(client *fakeLiveClaudeStream) *liveClaudeStreamTurn {
	live := &liveClaudeStreamTurn{client: client, binding: *f.task.NativeTurn, approval: &f.request}
	f.runtime.claudeStreamTurns[f.key] = live
	return live
}

func (f *claudeStreamApprovalFixture) status(t *testing.T) execution.ApprovalStatus {
	t.Helper()
	current, err := f.engine.ApprovalManager().GetApproval(f.record.ApprovalID)
	if err != nil {
		t.Fatalf("read canonical approval: %v", err)
	}
	return current.Status
}

// Property 1 (the security contract): a restart that dropped the local Claude
// process must refuse the approval outright. The canonical record must remain
// pending, because there is no proof a later native turn would be the same turn.
func TestNativeClaudeApprovalRefusesRestartWithoutLiveTurn(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		bind func(f *claudeStreamApprovalFixture)
	}{
		{name: "no live turn", bind: func(*claudeStreamApprovalFixture) {}},
		{name: "live turn without client", bind: func(f *claudeStreamApprovalFixture) {
			f.runtime.claudeStreamTurns[f.key] = &liveClaudeStreamTurn{binding: *f.task.NativeTurn, approval: &f.request}
		}},
		{name: "live turn without bound native request", bind: func(f *claudeStreamApprovalFixture) {
			f.runtime.claudeStreamTurns[f.key] = &liveClaudeStreamTurn{client: &fakeLiveClaudeStream{}, binding: *f.task.NativeTurn}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newClaudeStreamApprovalFixture(t)
			tc.bind(f)
			err := f.service.Approve(ctx, f.record.ApprovalID, "operator", "reviewed")
			if !errors.Is(err, model.ErrUnavailable) {
				t.Fatalf("approve without live Claude turn = %v, want model.ErrUnavailable", err)
			}
			if status := f.status(t); status != execution.ApprovalRequested {
				t.Fatalf("refused approval mutated canonical record: status=%s, want %s", status, execution.ApprovalRequested)
			}
			// A refused reattachment must equally never release the provider.
			if live := f.runtime.claudeStreamTurns[f.key]; live != nil && live.accepted {
				t.Fatal("refused approval marked the live turn accepted")
			}
		})
	}
}

// Property 2: with a live turn and a matching pending approval, approval marks
// the canonical record decided BEFORE the provider is told, calls the typed
// ResolveApproval exactly once, and flips the live accept marker.
func TestNativeClaudeApprovalDecidesRecordBeforeReleasingProvider(t *testing.T) {
	ctx := context.Background()
	f := newClaudeStreamApprovalFixture(t)
	client := &fakeLiveClaudeStream{}
	client.observe = func() execution.ApprovalStatus {
		current, err := f.engine.ApprovalManager().GetApproval(f.record.ApprovalID)
		if err != nil {
			return ""
		}
		return current.Status
	}
	live := f.bindLiveTurn(client)

	if err := f.service.Approve(ctx, f.record.ApprovalID, "operator", "reviewed exact request"); err != nil {
		t.Fatalf("approve live native Claude request: %v", err)
	}
	if client.resolved != 1 || client.declined != 0 {
		t.Fatalf("native decision = resolved:%d declined:%d, want 1/0", client.resolved, client.declined)
	}
	// Ordering: the durable record was already approved when the provider was
	// told. The provider is never released on an undecided record.
	if client.statusAtResolve != execution.ApprovalApproved {
		t.Fatalf("canonical record status when provider was told = %q, want %s", client.statusAtResolve, execution.ApprovalApproved)
	}
	if status := f.status(t); status != execution.ApprovalConsumed {
		t.Fatalf("native approval must be one-shot consumed: status=%s", status)
	}
	if !live.accepted {
		t.Fatal("live Claude turn was not marked accepted after the provider consumed the approval")
	}
	// Duplicate UI submission cannot cause a second native accept.
	if err := f.service.Approve(ctx, f.record.ApprovalID, "operator", "replay"); err == nil {
		t.Fatal("duplicate native Claude approval was accepted")
	}
	if client.resolved != 1 {
		t.Fatalf("duplicate approval called native accept %d times", client.resolved)
	}
}

// Property 3: rejection denies the durable record, declines exactly once
// through the typed client, and drops the live turn so nothing can reattach.
func TestNativeClaudeApprovalRejectionDeclinesAndDropsLiveTurn(t *testing.T) {
	ctx := context.Background()
	f := newClaudeStreamApprovalFixture(t)
	client := &fakeLiveClaudeStream{}
	f.bindLiveTurn(client)

	if err := f.service.Reject(ctx, f.record.ApprovalID, "operator", "not permitted"); err != nil {
		t.Fatalf("reject live native Claude request: %v", err)
	}
	if client.declined != 1 || client.resolved != 0 {
		t.Fatalf("native decision = declined:%d resolved:%d, want 1/0", client.declined, client.resolved)
	}
	if status := f.status(t); status != execution.ApprovalDenied {
		t.Fatalf("rejected approval status = %s, want %s", status, execution.ApprovalDenied)
	}
	if _, ok := f.runtime.claudeStreamTurns[f.key]; ok {
		t.Fatal("rejected native Claude turn remained live and reattachable")
	}
}

// Property 4: a provider-side failure to consume the approval propagates. The
// operator must see that the tool call was not released.
func TestNativeClaudeApprovalPropagatesResolveFailure(t *testing.T) {
	ctx := context.Background()
	f := newClaudeStreamApprovalFixture(t)
	boom := errors.New("claude stream process is gone")
	client := &fakeLiveClaudeStream{resolveErr: boom}
	live := f.bindLiveTurn(client)

	err := f.service.Approve(ctx, f.record.ApprovalID, "operator", "reviewed")
	if !errors.Is(err, boom) {
		t.Fatalf("approve with failing ResolveApproval = %v, want %v", err, boom)
	}
	if client.resolved != 0 {
		t.Fatalf("failed resolve counted as a native accept: %d", client.resolved)
	}
	if live.accepted {
		t.Fatal("live Claude turn was marked accepted despite a failed provider resolve")
	}
}

// Property 5a: cancelling with no live turn is a refusal, not a silent success.
func TestCancelClaudeStreamTurnRefusesUnboundCancellation(t *testing.T) {
	ctx := context.Background()
	engine, err := execution.NewEngine(execution.EngineConfig{ProjectRoot: t.TempDir()}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{claudeStreamTurns: make(map[string]*liveClaudeStreamTurn)}
	service := &ExecutionService{runtime: runtime, engine: engine, now: time.Now}
	if err := service.CancelClaudeStreamTurn(ctx, "run-claude-cancel", "task-claude-cancel", "replay"); !errors.Is(err, model.ErrUnavailable) {
		t.Fatalf("cancellation without a live Claude turn = %v, want model.ErrUnavailable", err)
	}
}

// Property 5b: cancellation interrupts the live turn exactly once, records the
// durable Process 05 cancellation, and drops the live turn so a replayed
// cancellation cannot send a second interrupt.
func TestCancelClaudeStreamTurnInterruptsOnceThenPersists(t *testing.T) {
	ctx := context.Background()
	engine, err := execution.NewEngine(execution.EngineConfig{ProjectRoot: t.TempDir()}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	worktree := t.TempDir()
	run := execution.ExecutionRun{RunID: "run-claude-cancel", Version: 1, State: execution.RunRunning, Tasks: map[string]execution.TaskExecution{
		"task-claude-cancel": {RunID: "run-claude-cancel", TaskID: "task-claude-cancel", State: execution.TaskRunning, NativeTurn: &execution.NativeTurnBinding{Provider: "claude-stream", ThreadID: "session-cancel", SessionID: "session-cancel", TurnID: "turn-cancel", Worktree: worktree, WorktreeDigest: "sha256:worktree", ConstraintDigest: "sha256:constraints", RunRevision: 1, State: "RUNNING"}},
	}}
	if err := engine.RunStore().CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	key := "run-claude-cancel\x00task-claude-cancel"
	client := &fakeLiveClaudeStream{}
	runtime := &Runtime{claudeStreamTurns: map[string]*liveClaudeStreamTurn{key: {client: client, binding: *run.Tasks["task-claude-cancel"].NativeTurn}}}
	service := &ExecutionService{runtime: runtime, engine: engine, now: time.Now}
	runtime.execService = service

	if err := service.CancelClaudeStreamTurn(ctx, "run-claude-cancel", "task-claude-cancel", "operator cancelled"); err != nil {
		t.Fatalf("typed native Claude cancellation: %v", err)
	}
	if client.interrupted != 1 {
		t.Fatalf("typed turn/interrupt calls = %d, want 1", client.interrupted)
	}
	if _, ok := runtime.claudeStreamTurns[key]; ok {
		t.Fatal("cancelled native Claude turn remained live")
	}
	current, err := engine.GetRun(ctx, "run-claude-cancel")
	if err != nil || current.State != execution.RunCancelled || current.Tasks["task-claude-cancel"].State != execution.TaskCancelled {
		t.Fatalf("cancellation was not durable: %#v %v", current, err)
	}
	if err := service.CancelClaudeStreamTurn(ctx, "run-claude-cancel", "task-claude-cancel", "replay"); !errors.Is(err, model.ErrUnavailable) {
		t.Fatalf("duplicate cancellation must not send another interrupt: %v", err)
	}
	if client.interrupted != 1 {
		t.Fatalf("duplicate cancellation interrupted %d times", client.interrupted)
	}
}

// A native Claude approval must never be decided as an ordinary task approval.
// Doing so would put the task back in READY while the provider turn is still
// live, releasing a tool call nobody consumed through the bridge.
func TestNativeClaudeApprovalCannotBeDecidedAsOrdinaryTaskApproval(t *testing.T) {
	engine, err := execution.NewEngine(execution.EngineConfig{ProjectRoot: t.TempDir()}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	record, err := engine.ApprovalManager().RequestApproval(execution.ApprovalRequest{
		RunID: "run-native", TaskID: "task-native", OperationType: claudeStreamApprovalOperation,
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
