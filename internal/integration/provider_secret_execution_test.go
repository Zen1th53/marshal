package integration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/secrets"
)

type echoSecretAdapter struct {
	fakeCommitAdapter
	secretToEcho string
}

func (a echoSecretAdapter) Run(ctx context.Context, request adapter.Request) (adapter.Result, error) {
	res, err := a.fakeCommitAdapter.Run(ctx, request)
	res.Stdout = []byte("Execution started with credential: " + a.secretToEcho + " - done.")
	res.Stderr = []byte("Diagnostics: key=" + a.secretToEcho)
	return res, err
}

func TestProviderSecretExecutionLeaseAndRedaction(t *testing.T) {
	const secretVal = "super-secret-runtime-token-998877"
	t.Setenv("MARSHAL_PROVIDER_KEY", secretVal)

	repo := runtimeIntegrationRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}

	rt, err := app.OpenWithOptions(context.Background(), repo.Path(), app.Options{
		Adapters: map[string]adapter.Adapter{
			"fake-secret": echoSecretAdapter{secretToEcho: secretVal},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	agent, err := rt.RegisterAgent(context.Background(), app.RegisterAgentRequest{
		Name: "secret-worker",
		Role: model.RoleDeveloper,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := rt.ImportTasks(context.Background(), []model.Task{
		{ID: "TASK-SEC-001", Title: "Test secret provider execution", Status: model.TaskReady, Risk: model.R1},
	}); err != nil {
		t.Fatal(err)
	}

	result, err := rt.Run(context.Background(), app.RunRequest{
		TaskID:  "TASK-SEC-001",
		AgentID: agent.ID,
		Adapter: "fake-secret",
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.ExitStatus != 0 {
		t.Fatalf("expected exit status 0, got %d", result.ExitStatus)
	}

	// 1. Verify artifact output is created and redacted
	artifacts, err := rt.Artifacts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) == 0 {
		t.Fatal("expected artifacts to be created")
	}

	// 2. Scan entire .marshal directory on disk to ensure raw secret is nowhere in SQLite or files
	scan := func(root string) error {
		return filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil || info == nil || info.IsDir() {
				return walkErr
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(data), secretVal) {
				t.Fatalf("raw secret value leaked into file: %s", path)
			}
			return nil
		})
	}
	if err := scan(filepath.Join(repo.Path(), ".marshal")); err != nil {
		t.Fatal(err)
	}

	// 3. Scan audit events to ensure raw secret is not in event bodies
	events, err := rt.Events(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	eventsJSON, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(eventsJSON), secretVal) {
		t.Fatal("raw secret value found in event logs")
	}

	// 4. Test use-after-revoke on secretBroker
	broker := rt.SecretBroker()
	if broker == nil {
		t.Fatal("expected non-nil secret broker")
	}

	// Attempting WithSecret on an expired/revoked lease fails
	revokedLease := secrets.Lease{
		ID:        "revoked-lease-id",
		Ref:       secrets.Ref{Provider: "env", Name: "MARSHAL_PROVIDER_KEY", Version: "1"},
		Subject:   agent.ID,
		TaskID:    "TASK-SEC-001",
		Purpose:   "provider_execution",
		State:     secrets.StateRevoked,
	}
	err = broker.WithSecret(context.Background(), revokedLease, func([]byte) error {
		t.Fatal("revoked lease callback should never be called")
		return nil
	})
	if err == nil {
		t.Fatal("expected error on revoked lease, got nil")
	}
}
