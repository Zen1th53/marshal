package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/netpolicy"
)

type e2eMockProviderAdapter struct {
	fakeCommitAdapter
	runs int
}

func (a *e2eMockProviderAdapter) Run(ctx context.Context, req adapter.Request) (adapter.Result, error) {
	a.runs++
	// Generate code in worktree
	genFile := filepath.Join(req.Worktree, "src", "solution.go")
	_ = os.MkdirAll(filepath.Dir(genFile), 0755)
	_ = os.WriteFile(genFile, []byte("package src\n\nfunc Solved() bool { return true }\n"), 0644)

	return adapter.Result{
		Status:   adapter.StatusSuccess,
		ExitCode: 0,
		Stdout:   []byte("E2E provider generated solution successfully\n"),
		Stderr:   nil,
	}, nil
}

func TestProviderExecutionE2EChain(t *testing.T) {
	ctx := context.Background()
	repo := runtimeIntegrationRepo(t)
	if _, err := app.Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	mockAdapter := &e2eMockProviderAdapter{}
	runtime, err := app.OpenWithOptions(ctx, repo.Path(), app.Options{
		Adapters: map[string]adapter.Adapter{"codex": mockAdapter},
	})
	if err != nil {
		t.Fatalf("OpenWithOptions: %v", err)
	}
	defer runtime.Close()

	// 1. Register Developer Agent
	agent, err := runtime.RegisterAgent(ctx, app.RegisterAgentRequest{
		Name: "e2e-developer",
		Role: model.RoleDeveloper,
	})
	if err != nil {
		t.Fatalf("RegisterAgent: %v", err)
	}

	// 2. Import Task
	_, err = runtime.ImportTasks(ctx, []model.Task{
		{
			ID:     "TASK-E2E-001",
			Title:  "Implement Solved function",
			Status: model.TaskReady,
			Risk:   model.R1,
		},
	})
	if err != nil {
		t.Fatalf("ImportTasks: %v", err)
	}

	// 3. Execute RunRequest with Network & Egress rules through entire chain
	egressRules := []netpolicy.Rule{
		{
			ID:          "rule-pkg-go",
			HostPattern: "proxy.golang.org",
			Protocol:    netpolicy.ProtocolTCP,
			Ports:       []int{443},
			Action:      netpolicy.ActionAllow,
		},
	}

	runResult, err := runtime.Run(ctx, app.RunRequest{
		TaskID:          "TASK-E2E-001",
		AgentID:         agent.ID,
		Adapter:         "codex",
		NetworkRequired: true,
		EgressRules:     egressRules,
	})
	if err != nil {
		t.Fatalf("runtime.Run failed: %v", err)
	}

	// 4. Verify Execution Outputs
	if runResult.Status != "success" {
		t.Fatalf("expected run status success, got %s", runResult.Status)
	}
	if runResult.ResultCommit == "" || runResult.ResultCommit == runResult.BaseCommit {
		t.Fatalf("expected new result commit created on worktree branch, got base=%s result=%s", runResult.BaseCommit, runResult.ResultCommit)
	}

	// 5. Verify Capability Grants were created and cleaned up
	st := runtime.Store()
	grants, err := st.ListCapabilityGrants(ctx)
	if err != nil {
		t.Fatalf("ListCapabilityGrants: %v", err)
	}
	for _, g := range grants {
		if g.Subject == capability.SubjectID(agent.ID) && g.TaskID == "TASK-E2E-001" {
			if g.RevokedAt == nil {
				t.Fatalf("expected capability grant %s to have RevokedAt set after run completion", g.ID)
			}
		}
	}
}
