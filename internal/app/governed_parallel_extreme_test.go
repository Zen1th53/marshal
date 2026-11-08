//go:build !governed

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
	"github.com/Zen1th53/marshal/internal/adapter/claude"
	"github.com/Zen1th53/marshal/internal/adapter/codex"
	"github.com/Zen1th53/marshal/internal/adapter/opencode"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/sessionmcp"
)

type lanePreparedProvider struct {
	provider string
	started  chan string
	release  <-chan struct{}
}

func (r *lanePreparedProvider) Run(ctx context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
	result := adapter.ProcessResult{StartedAt: time.Now().UTC(), EndedAt: time.Now().UTC()}
	if len(cmd.Args) == 1 && cmd.Args[0] == "--version" {
		result.Stdout = []byte("1.0.0")
		return result, nil
	}
	if strings.Contains(strings.Join(cmd.Args, " "), "--help") {
		result.Stdout = []byte("--json --sandbox --ephemeral --ignore-user-config --cd --output-format --permission-mode --permission-prompts --strict-mcp-config --add-dir --resume")
		return result, nil
	}
	// A launched provider acknowledges launch (releasing the dispatch barrier
	// and run lock) before it does any work, so lanes genuinely overlap.
	execution.AcknowledgeWorkerLaunch(ctx)
	if cmd.Heartbeat != nil {
		cmd.Heartbeat()
	}
	r.started <- r.provider
	select {
	case <-r.release:
	case <-ctx.Done():
		return result, ctx.Err()
	}
	if err := os.WriteFile(filepath.Join(cmd.Dir, r.provider+".txt"), []byte("done\n"), 0600); err != nil {
		return result, err
	}
	if r.provider == "opencode" {
		result.Stdout = []byte(`{"type":"text","part":{"text":"done"}}` + "\n")
	} else {
		result.Stdout = []byte(`{"result":"done"}` + "\n")
	}
	return result, nil
}

// Actual Runtime-owned Process05 executor calls prepared provider adapters. This
// is a deterministic integration test, not a claim of paid native execution.
func TestExtremeGovernedRuntimeThreeProvidersOverlap(t *testing.T) {
	specs := make(chan sessionmcp.WorkerHostSpec, 3)
	ctx := sessionmcp.WithWorkerServerFactory(t.Context(), func(spec sessionmcp.WorkerHostSpec) (sessionmcp.WorkerServer, error) {
		specs <- spec
		return func(context.Context, io.Reader, io.Writer) error { return nil }, nil
	})
	repo := runtimeRepo(t)
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	started := make(chan string, 3)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	clients := map[string]adapter.Adapter{}
	for _, provider := range []string{"codex", "claude", "opencode"} {
		runner := &lanePreparedProvider{provider: provider, started: started, release: release}
		switch provider {
		case "codex":
			clients[provider] = codex.NewWithModels("/prepared/codex", runner, []codex.ModelInfo{{Slug: "test-model"}}, "test-model")
		case "claude":
			clients[provider] = claude.New("/prepared/claude", runner)
		case "opencode":
			clients[provider] = opencode.NewWithModel("/prepared/opencode", runner, "test-model")
		}
	}
	runtime, err := OpenWithOptions(ctx, repo.Path(), Options{Adapters: clients})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	requiredTestAcceptedProjectContract(t, runtime)
	goal := planGoal()
	goal.ProjectID = requiredProcess05TestProjectID(runtime, goal.ProjectID)
	goal.Risk = model.R1
	goal.SuccessCriteria = []string{"outputs exist"}
	if err = runtime.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	req := CreatePlanRequest{SessionID: goal.SessionID, ProjectID: projectid.ID(goal.ProjectID), Mode: plan.ModeUltra}
	for _, provider := range []string{"codex", "claude", "opencode"} {
		capacity := goalintake.UnknownCapacity(provider, true)
		req.Tasks = append(req.Tasks, plan.Task{ID: provider, Title: "write " + provider + ".txt", Mutating: true, Paths: []string{provider + ".txt"}, Criteria: goal.SuccessCriteria})
		req.Candidates = append(req.Candidates, goalintake.Candidate{Provider: provider, Model: "test-model", Capacity: capacity, Governance: constitution.GovernanceVerified})
		req.HarnessCandidates = append(req.HarnessCandidates, plan.HarnessCandidate{Profile: model.HarnessProfile{Harness: provider, InstalledVersion: "1.0.0", SupportedModels: []string{"test-model"}, DefaultModel: "test-model", ProbeEvidenceID: "EV-provider", ProbedAt: time.Now().UTC()}, InstalledVersion: "1.0.0", Provider: provider, Capacity: capacity})
		if _, err = runtime.RegisterAgent(ctx, RegisterAgentRequest{Name: provider, Role: model.RoleDeveloper, ModelProvider: provider}); err != nil {
			t.Fatal(err)
		}
	}
	p, err := runtime.Plans().Create(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	p.Assignments.Assignments = nil
	for _, provider := range []string{"codex", "claude", "opencode"} {
		p.Assignments.Assignments = append(p.Assignments.Assignments, plan.Assignment{Role: plan.RoleDeveloper, Tasks: []string{provider}, Harness: provider, Provider: provider, Model: "test-model", Governance: constitution.GovernanceVerified})
		p.Routes[provider] = plan.Route{Provider: provider, Model: "test-model", Governance: constitution.GovernanceVerified}
	}
	p.Approvals = nil
	if err = runtime.Store().SavePlan(ctx, p, p.Version); err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.Plans().Approve(ctx, projectid.ID(goal.ProjectID)); err != nil {
		t.Fatal(err)
	}
	service := runtime.Execution()
	parent, err := service.StartRun(ctx, goal.SessionID, projectid.ID(goal.ProjectID))
	if err != nil {
		t.Fatal(err)
	}
	base := repo.HEAD(t)
	if err = service.Engine().SetTaskLaneParent(ctx, parent.RunID, base); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 3)
	lanes := map[string]string{}
	for _, provider := range []string{"codex", "claude", "opencode"} {
		lane, err := service.Engine().OpenTaskLane(ctx, parent.RunID, provider, base)
		if err != nil {
			t.Fatal(err)
		}
		lanes[provider] = lane.RunID
	}
	for _, provider := range []string{"codex", "claude", "opencode"} {
		go func(id, task string) {
			run, err := service.ExecuteTaskBound(ctx, id, task, base)
			if err != nil && run != nil {
				err = fmt.Errorf("%w: %s", err, run.Tasks[task].LastFailureReason)
			}
			if err == nil && run.Tasks[task].State != execution.TaskCompletedPendingVerify {
				t.Errorf("%s state=%s", task, run.Tasks[task].State)
			}
			done <- err
		}(lanes[provider], provider)
	}
	seen := map[string]bool{}
	for range 3 {
		select {
		case provider := <-started:
			seen[provider] = true
		case err := <-done:
			t.Fatalf("governed executor refused before provider overlap: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("governed provider executors did not overlap")
		}
	}
	if len(seen) != 3 {
		t.Fatal("not all governed providers reached process boundary")
	}
	var bindings []sessionmcp.WorkerHostSpec
	principals := map[string]bool{}
	for range 3 {
		spec := <-specs
		bindings = append(bindings, spec)
		if spec.Runtime != runtime || spec.Root != runtime.ProjectRoot() || spec.PrincipalID == "worker" || spec.TaskID == "" || spec.Validate(ctx) != nil {
			t.Fatal("admitted Process05 worker lost authentic runtime/task/principal binding")
		}
		principals[spec.PrincipalID] = true
		proofCtx := spec.RequestContext(ctx)
		if allowed, err := runtime.authorizeBoundWorkerSourceRead(proofCtx, spec.PrincipalID, spec.TaskID); err != nil || !allowed {
			t.Fatalf("approved source read required fabricated memory grant: %v", err)
		}
		readOnly := authz.Principal{ID: spec.PrincipalID, Role: authz.Role{Name: "worker", Authorities: []authz.Authority{authz.AuthorityTaskPlan}}}
		if _, err := runtime.Memory().ListTaskSlots(proofCtx, readOnly, runtime.ProjectID(), spec.TaskID); err == nil {
			t.Fatal("derived source attestation granted working-memory slots")
		}
		if allowed, err := runtime.authorizeBoundWorkerSourceRead(proofCtx, "marshal", spec.TaskID); err == nil || allowed {
			t.Fatal("worker context allowed principal substitution")
		}
		if allowed, err := runtime.authorizeBoundWorkerSourceRead(proofCtx, spec.PrincipalID, "TASK-foreign"); err == nil || allowed {
			t.Fatal("worker context allowed canonical task substitution")
		}
	}
	if len(principals) != 3 {
		t.Fatal("provider workers shared a synthetic principal")
	}
	// Canonical registered identity changes revoke an existing host binding,
	// without replacing the caller from a model-supplied role or argument.
	chosen := bindings[0]
	agent, err := runtime.Store().GetAgent(ctx, chosen.PrincipalID)
	if err != nil {
		t.Fatal(err)
	}
	originalProvider := agent.ModelProvider
	agent.ModelProvider = "foreign-provider"
	agent, err = runtime.Store().UpdateAgent(ctx, agent, agent.Revision)
	if err != nil || chosen.Validate(ctx) == nil {
		t.Fatal("reassigned provider kept old worker authority")
	}
	agent.ModelProvider = originalProvider
	agent.Status = model.AgentDisabled
	agent, err = runtime.Store().UpdateAgent(ctx, agent, agent.Revision)
	if err != nil || chosen.Validate(ctx) == nil {
		t.Fatal("disabled agent kept worker authority")
	}
	agent.Status = model.AgentRegistered
	if _, err = runtime.Store().UpdateAgent(ctx, agent, agent.Revision); err != nil {
		t.Fatal(err)
	}
	for range 20 {
		replayed, err := service.StartRun(ctx, goal.SessionID, projectid.ID(goal.ProjectID))
		if err != nil || replayed.RunID != parent.RunID || replayed.ParentRunID != "" {
			t.Fatalf("plan replay selected child lane instead of canonical parent: %v", err)
		}
	}
	// Hold one final revocation through Finish; temporary revocation above
	// may be observed by the live watcher, but is not a timing assertion.
	agent, err = runtime.Store().GetAgent(ctx, chosen.PrincipalID)
	if err != nil {
		t.Fatal(err)
	}
	agent.Status = model.AgentDisabled
	if _, err = runtime.Store().UpdateAgent(ctx, agent, agent.Revision); err != nil {
		t.Fatal(err)
	}
	unblock()
	refused := 0
	for range 3 {
		if err := <-done; err != nil {
			if !strings.Contains(err.Error(), "REQUIRED_CONTEXT_CHANGED") {
				t.Fatal(err)
			}
			refused++
		}
	}
	if refused == 0 {
		t.Fatal("held live identity revocation failed to quarantine the provider result")
	}
	for _, spec := range bindings {
		if spec.Validate(ctx) == nil {
			t.Fatal("closed provider attempt retained worker authority")
		}
	}
}

func TestExtremeStandardApprovedPlanNeverParallelFromUltraTier(t *testing.T) {
	s, _ := setupExtremeService(t, 2)
	run := marshal.Run{Process05Bound: true, Tier: marshal.Ultra, Settings: marshal.DefaultSettings(), Tasks: []marshal.Task{{PlanTaskID: "a", Mode: marshal.Governed, Worker: "codex", Files: []string{"a"}, State: marshal.Dispatched}, {PlanTaskID: "b", Mode: marshal.Governed, Worker: "claude", Files: []string{"b"}, State: marshal.Queued}}}
	if err := checkDispatchAdmission(s.Gate, run, run.Tasks[1], 1); err == nil || !shouldStopAfterLaunchGate(s.Gate, run, run.Tasks[1]) {
		t.Fatal("tier alone enabled parallel approved Standard plan")
	}
	bindPrivateProcess05Tier(&run, plan.ExecutionPlan{Mode: plan.ModeStandard})
	if run.Process05Parallel {
		t.Fatal("standard plan gained parallel capability")
	}
	bindPrivateProcess05Tier(&run, plan.ExecutionPlan{Mode: plan.ModeUltra})
	if !run.Process05Parallel || checkDispatchAdmission(s.Gate, run, run.Tasks[1], 1) != nil || shouldStopAfterLaunchGate(s.Gate, run, run.Tasks[1]) {
		t.Fatal("approved independent ULTRA tasks did not admit parallel")
	}
	bindPrivateProcess05Tier(&run, plan.ExecutionPlan{Mode: plan.ModeStandard})
	if run.Process05Parallel || checkDispatchAdmission(s.Gate, run, run.Tasks[1], 1) == nil {
		t.Fatal("standard rebind retained stale parallel capability")
	}
}
