package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/sandbox"
)

type credentialProbingAdapter struct {
	fakeCommitAdapter
	sensitivePathToProbe string
	probedSuccessfully   bool
}

func (a *credentialProbingAdapter) Run(ctx context.Context, request adapter.Request) (adapter.Result, error) {
	res, err := a.fakeCommitAdapter.Run(ctx, request)
	if _, statErr := os.Stat(a.sensitivePathToProbe); statErr == nil {
		a.probedSuccessfully = true
	}
	return res, err
}

func TestSandboxIsolationBlocksUndeclaredHostCredentials(t *testing.T) {
	bwrapPath, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("bwrap not available on host system")
	}
	bwrap := sandbox.NewBwrap(bwrapPath)
	if cap := bwrap.Probe(context.Background()); !cap.Available {
		t.Skip("bwrap probe failed: " + cap.Reason)
	}

	fakeSSHDir := filepath.Join(t.TempDir(), ".ssh")
	_ = os.MkdirAll(fakeSSHDir, 0o700)
	fakeKey := filepath.Join(fakeSSHDir, "id_rsa")
	_ = os.WriteFile(fakeKey, []byte("fake-private-key"), 0o600)

	// Wrap sandbox request
	worktree := t.TempDir()
	spec, err := bwrap.Wrap(model.SandboxRequest{
		Worktree:       worktree,
		NetworkAllowed: false,
		ReadOnlyBinds: []model.Bind{
			{Source: fakeKey, Target: "/home/marshal/.ssh/id_rsa"},
		},
	}, []string{"/bin/sh", "-c", "cat /home/marshal/.ssh/id_rsa"})

	// Wrap must reject the forbidden credential bind
	if err == nil {
		t.Fatalf("expected error wrapping forbidden .ssh bind, got spec: %#v", spec)
	}
}

func TestHighRiskNetworkRequestWithoutAuthorizationFailsClosed(t *testing.T) {
	repo := runtimeIntegrationRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}

	rt, err := app.OpenWithOptions(context.Background(), repo.Path(), app.Options{
		Adapters: map[string]adapter.Adapter{
			"fake": fakeCommitAdapter{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	agent, err := rt.RegisterAgent(context.Background(), app.RegisterAgentRequest{
		Name: "dev-worker",
		Role: model.RoleDeveloper,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := rt.ImportTasks(context.Background(), []model.Task{
		{ID: "TASK-HIGH-RISK", Title: "High risk task with network", Status: model.TaskReady, Risk: model.R2},
	}); err != nil {
		t.Fatal(err)
	}

	// Developer role on R2 risk requesting network without explicit policy authorization must fail closed
	_, err = rt.Run(context.Background(), app.RunRequest{
		TaskID:          "TASK-HIGH-RISK",
		AgentID:         agent.ID,
		Adapter:         "fake",
		NetworkRequired: true,
	})
	if err == nil {
		t.Fatal("expected policy rejection for high-risk network access without authorization, got nil")
	}
}
