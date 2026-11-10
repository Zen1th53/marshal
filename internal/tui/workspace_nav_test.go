package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/testutil/testcloud"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

// entitleULTRA gives a test workspace a genuinely signed, verified ULTRA lease.
// The lease travels cloud.Gate.Adopt, so this grants entitlement rather than
// bypassing the check for it.
func entitleULTRA(t *testing.T, ws *Workspace) {
	t.Helper()
	ws.AttachULTRA(testcloud.EntitledGate(t, testcloud.Options{}), true)
}

// realControlWorkspace constructs the real Workspace over a bootstrapped
// runtime, exactly as the CLI does, for tests that exercise canonical authority
// behaviour. The variadic parameter is retained for callers; it no longer
// controls any navigation surface.
func realControlWorkspace(t *testing.T, sessionID string, _ ...bool) (*Workspace, *app.Runtime) {
	t.Helper()
	repo := testgit.New(t)
	for _, name := range []string{"CAPABILITIES.yaml", "PACK-VERSION.yaml", "RUNTIME-VERSION.yaml"} {
		data, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo.Path(), name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	status, err := runtime.Status(context.Background())
	if err != nil {
		t.Fatalf("runtime status: %v", err)
	}
	ws := NewWorkspace(runtime.Store(), string(status.Project.ID), sessionID)
	ws.workDir = runtime.ProjectRoot()
	t.Cleanup(func() { ws.Close(); _ = ws.runtime.Close() })
	ws.AttachRuntime(runtime, projectid.ID(runtime.ProjectIdentity()))
	ws.tmuxSession = fixtureTmuxNamespace(t, runtime.ProjectRoot())
	entitleULTRA(t, ws)
	return ws, runtime
}

// TestRestoreVerificationRequiresCanonicalProjectIdentity prevents a caller
// from turning an unattached runtime into a cross-project restore oracle.
func TestRestoreVerificationRequiresCanonicalProjectIdentity(t *testing.T) {
	_, runtime := realControlWorkspace(t, "SESSION-tui-restore-identity")
	authority := &runtimeControlAuthority{runtime: runtime}
	if _, err := authority.VerifyStateBackup(context.Background(), "unused.db"); err == nil ||
		!strings.Contains(err.Error(), "canonical project identity") {
		t.Fatalf("restore verification without project identity = %v, want fail-closed identity error", err)
	}
}

// Dispatching a Codex task is never a shortcut around Work.  In particular,
// a blank form must not manufacture an unplanned task or a synthetic worker
// agent merely because the user pressed Enter.  Both identities have to exist
// durably before Process 05 can be asked to run anything.
func TestCodexDispatchRefusesMissingCanonicalTaskAndAgent(t *testing.T) {
	ctx := context.Background()
	_, runtime := realControlWorkspace(t, "SESSION-tui-codex-dispatch-refusal")
	authority := &runtimeControlAuthority{runtime: runtime, store: runtime.Store()}

	if _, err := authority.DispatchCodexTask(ctx, CodexTaskDispatchRequest{}); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("blank Codex dispatch error = %v, want ErrInvalid", err)
	}
	if got := mustCount(t, runtime.Store(), "tasks"); got != 0 {
		t.Fatalf("blank Codex dispatch created %d tasks", got)
	}
	if got := mustCount(t, runtime.Store(), "agents"); got != 0 {
		t.Fatalf("blank Codex dispatch created %d agents", got)
	}
	if got := mustCount(t, runtime.Store(), "sessions"); got != 0 {
		t.Fatalf("blank Codex dispatch created %d sessions", got)
	}

	if _, err := runtime.ImportTasks(ctx, []model.Task{{
		ID: "TASK-tui-codex-existing", Title: "preplanned Codex task", Status: model.TaskReady, Risk: model.R1,
	}}); err != nil {
		t.Fatalf("import planned task: %v", err)
	}
	if _, err := authority.DispatchCodexTask(ctx, CodexTaskDispatchRequest{TaskID: "TASK-tui-codex-existing"}); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("dispatch without agent error = %v, want ErrInvalid", err)
	}
	if got := mustCount(t, runtime.Store(), "agents"); got != 0 {
		t.Fatalf("dispatch without agent created %d agents", got)
	}
	if got := mustCount(t, runtime.Store(), "sessions"); got != 0 {
		t.Fatalf("dispatch without agent created %d sessions", got)
	}
	wrongProvider, err := runtime.RegisterAgent(ctx, app.RegisterAgentRequest{Name: "local claude", Role: model.RoleDeveloper, ModelProvider: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authority.DispatchCodexTask(ctx, CodexTaskDispatchRequest{TaskID: "TASK-tui-codex-existing", AgentID: wrongProvider.ID}); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("non-Codex agent must be refused before Process 05 dispatch: %v", err)
	}
	if got := mustCount(t, runtime.Store(), "sessions"); got != 0 {
		t.Fatalf("wrong-provider dispatch created %d sessions", got)
	}
}

func TestCodexDispatchUsesOnlyUniqueRegisteredCodexAgent(t *testing.T) {
	ctx := context.Background()
	_, runtime := realControlWorkspace(t, "SESSION-tui-codex-default-agent")
	authority := &runtimeControlAuthority{runtime: runtime, store: runtime.Store()}
	if _, err := runtime.ImportTasks(ctx, []model.Task{{
		ID: "TASK-tui-codex-default", Title: "preplanned Codex task", Status: model.TaskReady, Risk: model.R1,
	}}); err != nil {
		t.Fatal(err)
	}
	agent, err := runtime.RegisterAgent(ctx, app.RegisterAgentRequest{Name: "Local Codex", Role: model.RoleDeveloper, ModelProvider: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := authority.DefaultCodexDispatchAgent(ctx)
	if err != nil || got != agent.ID {
		t.Fatalf("unique Codex agent = %q, %v; want %q", got, err, agent.ID)
	}
	source := &ControlSource{Authority: authority}
	target, err := source.prepareCodexDispatch(ctx, ActionRequest{Inputs: map[string]string{"task_id": "TASK-tui-codex-default"}})
	if err != nil || target.ActorID != agent.ID {
		t.Fatalf("dispatch preflight did not bind unique agent: target=%#v err=%v", target, err)
	}
	if _, err := runtime.RegisterAgent(ctx, app.RegisterAgentRequest{Name: "Other Codex", Role: model.RoleDeveloper, ModelProvider: "codex"}); err != nil {
		t.Fatal(err)
	}
	if _, err := authority.DefaultCodexDispatchAgent(ctx); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("ambiguous Codex agents must require an operator choice: %v", err)
	}
}

func TestNativeCodexApprovalAppearsInCanonicalControlQueue(t *testing.T) {
	ctx := context.Background()
	_, runtime := realControlWorkspace(t, "SESSION-tui-native-codex-approval")
	authority := &runtimeControlAuthority{runtime: runtime, store: runtime.Store(), sessionID: "SESSION-tui-native-codex-approval"}
	manager := runtime.Execution().Engine().ApprovalManager()
	record, err := manager.RequestApproval(execution.ApprovalRequest{
		RunID: "run-native", TaskID: "task-native", PlanID: "plan-native", PlanVersion: 1,
		OperationType: "CODEX_APP_SERVER_NATIVE", TargetResource: "/safe/worktree",
		Scope:       "thread=one turn=one item=one method=item/commandExecution/requestApproval digest=sha256:one",
		DiffPreview: "item/commandExecution/requestApproval", Parameters: "sha256:one", CurrentState: "bound-native-state",
	})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := authority.PendingApprovals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, approval := range pending {
		if approval.ApprovalID == record.ApprovalID {
			found = true
		}
	}
	if found {
		t.Fatalf("orphan native Codex approval %s leaked into this session's Control queue: %#v", record.ApprovalID, pending)
	}
	if err := authority.DecideApproval(ctx, record.ApprovalID, true, "operator", "reviewed native request"); err == nil {
		t.Fatal("orphan native approval must not be decided without its Process 05 run")
	}
	current, err := manager.GetApproval(record.ApprovalID)
	if err != nil || current.Status != execution.ApprovalRequested {
		t.Fatalf("orphan decision attempt must not mutate approval: current=%#v err=%v", current, err)
	}
}

func mustCount(t *testing.T, st *store.Store, table string) int {
	t.Helper()
	count, err := st.Count(context.Background(), table)
	if err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}
