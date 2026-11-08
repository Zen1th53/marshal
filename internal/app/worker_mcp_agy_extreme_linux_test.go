//go:build !governed && linux

package app

import (
	"context"
	"fmt"
	"github.com/Zen1th53/marshal/internal/projectid"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/antigravity"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/sessionmcp"
)

type agyBoundWorkerRunner struct {
	started chan model.SandboxRequest
	release <-chan struct{}
}

func (r *agyBoundWorkerRunner) Run(ctx context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
	result := adapter.ProcessResult{StartedAt: time.Now().UTC()}
	if len(cmd.Args) == 1 && cmd.Args[0] == "--version" {
		result.Stdout = []byte("1.2.14")
		result.EndedAt = time.Now().UTC()
		return result, nil
	}
	if len(cmd.Args) == 1 && cmd.Args[0] == "--help" {
		result.Stdout = []byte("--print --output-format --conversation --mode --sandbox --new-project --disable-slash-commands --model")
		result.EndedAt = time.Now().UTC()
		return result, nil
	}
	sealed, err := sessionmcp.SandboxRequest(ctx, model.SandboxRequest{Worktree: cmd.Dir})
	if err != nil {
		return result, err
	}
	// The prepared native process has launched: acknowledge so the dispatch
	// barrier releases the store connection before the worker uses its relay.
	execution.AcknowledgeWorkerLaunch(ctx)
	r.started <- sealed
	select {
	case <-r.release:
	case <-ctx.Done():
		return result, ctx.Err()
	}
	if err := os.WriteFile(filepath.Join(cmd.Dir, "agy.txt"), []byte("done\n"), 0600); err != nil {
		return result, err
	}
	result.Stdout = []byte(`{"conversation_id":"synthetic-agy-session","status":"success","response":"done"}`)
	result.EndedAt = time.Now().UTC()
	return result, nil
}

// This uses an actual admitted Process05 task and the governed AGY adapter's
// generated relay/mask path with a model-free prepared process runner.
func TestExtremeAGYBoundWorkerUsesLiveAttemptAndSeparateWorktree(t *testing.T) {
	specs := make(chan sessionmcp.WorkerHostSpec, 1)
	ctx := sessionmcp.WithWorkerServerFactory(t.Context(), func(spec sessionmcp.WorkerHostSpec) (sessionmcp.WorkerServer, error) {
		specs <- spec
		return func(context.Context, io.Reader, io.Writer) error { return nil }, nil
	})
	repo := runtimeRepo(t)
	if err := os.Mkdir(filepath.Join(repo.Path(), ".agents"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo.Path(), ".agents", ".gitkeep"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", ".agents"}, {"commit", "-m", "tracked AGY configuration parent"}} {
		if _, err := gitMarshal(ctx, repo.Path(), args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	runner := &agyBoundWorkerRunner{started: make(chan model.SandboxRequest, 1), release: release}
	runtime, err := OpenWithOptions(ctx, repo.Path(), Options{Adapters: map[string]adapter.Adapter{"agy": antigravity.NewGoverned("/prepared/agy", runner)}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { unblock(); runtime.Close() }()
	requiredTestAcceptedProjectContract(t, runtime)
	goal := planGoal()
	goal.ProjectID = requiredProcess05TestProjectID(runtime, goal.ProjectID)
	goal.SuccessCriteria = []string{"AGY output exists"}
	if err := runtime.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	agent, err := runtime.RegisterAgent(ctx, RegisterAgentRequest{Name: "AGY", Role: model.RoleDeveloper, ModelProvider: "agy"})
	if err != nil {
		t.Fatal(err)
	}
	capacity := goalintake.UnknownCapacity("agy", true)
	p, err := runtime.Plans().Create(ctx, CreatePlanRequest{SessionID: goal.SessionID, ProjectID: projectid.ID(goal.ProjectID), Mode: plan.ModeUltra,
		Tasks:             []plan.Task{{ID: "agy", Title: "write agy.txt", Mutating: true, Paths: []string{"agy.txt"}, Criteria: goal.SuccessCriteria}},
		Candidates:        []goalintake.Candidate{{Provider: "agy", Model: "test-model", Capacity: capacity, Governance: constitution.GovernanceVerified}},
		HarnessCandidates: []plan.HarnessCandidate{{Profile: model.HarnessProfile{Harness: "antigravity", InstalledVersion: "1.2.14", SupportedModels: []string{"test-model"}, DefaultModel: "test-model", ProbeEvidenceID: "EV-synthetic", ProbedAt: time.Now().UTC()}, InstalledVersion: "1.2.14", Provider: "agy", Capacity: capacity}},
	})
	if err != nil {
		t.Fatal(err)
	}
	p.Assignments.Assignments = []plan.Assignment{{Role: plan.RoleDeveloper, Tasks: []string{"agy"}, Harness: "antigravity", Provider: "agy", Model: "test-model", Governance: constitution.GovernanceVerified}}
	p.Routes["agy"] = plan.Route{Provider: "agy", Model: "test-model", Governance: constitution.GovernanceVerified}
	p.Approvals = nil
	if err := runtime.Store().SavePlan(ctx, p, p.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Plans().Approve(ctx, projectid.ID(goal.ProjectID)); err != nil {
		t.Fatal(err)
	}
	service := runtime.Execution()
	parent, err := service.StartRun(ctx, goal.SessionID, projectid.ID(goal.ProjectID))
	if err != nil {
		t.Fatal(err)
	}
	base := repo.HEAD(t)
	if err := service.Engine().SetTaskLaneParent(ctx, parent.RunID, base); err != nil {
		t.Fatal(err)
	}
	lane, err := service.Engine().OpenTaskLane(ctx, parent.RunID, "agy", base)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		completed, err := service.ExecuteTaskBound(ctx, lane.RunID, "agy", base)
		if err != nil && completed != nil {
			err = fmt.Errorf("%w: %s", err, completed.Tasks["agy"].LastFailureReason)
		}
		if err == nil && completed.Tasks["agy"].State != execution.TaskCompletedPendingVerify {
			err = fmt.Errorf("AGY state=%s", completed.Tasks["agy"].State)
		}
		done <- err
	}()
	var sealed model.SandboxRequest
	select {
	case sealed = <-runner.started:
	case err := <-done:
		t.Fatalf("actual bound AGY attempt refused before provider process: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("AGY attempt never reached prepared native process")
	}
	spec := <-specs
	if spec.Root != runtime.ProjectRoot() || spec.PrincipalID != agent.ID || spec.TaskID == "" || spec.Validate(ctx) != nil {
		t.Fatal("AGY lost canonical source/task/principal authority")
	}
	if sealed.Worktree == spec.Root || sealed.WorkerMCPMask.Empty() || !sealed.WorkerMCPMask.Valid(sealed.Worktree) {
		t.Fatal("AGY invocation did not seal its separate task worktree")
	}
	proof := spec.RequestContext(ctx)
	if allowed, err := runtime.authorizeBoundWorkerSourceRead(proof, agent.ID, spec.TaskID); err != nil || !allowed {
		t.Fatal("live AGY source read refused")
	}
	for _, pair := range [][2]string{{"foreign-agent", spec.TaskID}, {agent.ID, "foreign-task"}} {
		if allowed, err := runtime.authorizeBoundWorkerSourceRead(proof, pair[0], pair[1]); err == nil || allowed {
			t.Fatal("AGY binding allowed principal/task substitution")
		}
	}
	for _, change := range []func(*model.Agent){func(a *model.Agent) { a.ModelProvider = "codex" }, func(a *model.Agent) { a.Status = model.AgentDisabled }} {
		current, err := runtime.Store().GetAgent(ctx, agent.ID)
		if err != nil {
			t.Fatal(err)
		}
		original := current
		change(&current)
		updated, err := runtime.Store().UpdateAgent(ctx, current, current.Revision)
		if err != nil || spec.Validate(ctx) == nil {
			t.Fatal("AGY binding survived provider/eligibility revocation")
		}
		original.Revision = updated.Revision
		if _, err := runtime.Store().UpdateAgent(ctx, original, original.Revision); err != nil {
			t.Fatal(err)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if spec.Validate(cancelled) == nil {
		t.Fatal("cancelled AGY callback retained authority")
	}
	current, err := runtime.Store().GetAgent(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	current.Status = model.AgentDisabled
	if _, err = runtime.Store().UpdateAgent(ctx, current, current.Revision); err != nil {
		t.Fatal(err)
	}
	unblock()
	if err := <-done; err == nil || !strings.Contains(err.Error(), "REQUIRED_CONTEXT_CHANGED") {
		t.Fatal("live identity revocation failed to quarantine the AGY result", err)
	}
	if spec.Validate(ctx) == nil {
		t.Fatal("completed AGY attempt retained worker authority")
	}
}
