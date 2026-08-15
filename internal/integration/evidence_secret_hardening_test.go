package integration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/model"
)

const a07ProviderSecretMarker = "MARSHAL_TEST_SECRET_T06_A07_PROVIDER_5f4d"

type a07SecretAdapter struct{ fakeCommitAdapter }

func (a07SecretAdapter) Run(ctx context.Context, request adapter.Request) (adapter.Result, error) {
	result, err := (fakeCommitAdapter{}).Run(ctx, request)
	result.Stdout = []byte(a07ProviderSecretMarker)
	result.Stderr = []byte(a07ProviderSecretMarker)
	return result, err
}

func TestRuntimeRejectsSecretProviderOutputBeforeArtifactPersistence(t *testing.T) {
	repo := runtimeIntegrationRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	rt, err := app.OpenWithOptions(context.Background(), repo.Path(), app.Options{
		Adapters: map[string]adapter.Adapter{"secret": a07SecretAdapter{}},
		EvidenceSanitizer: evidence.NewStrictSanitizer(evidence.SanitizerConfig{
			LiteralSecrets: []string{a07ProviderSecretMarker},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := rt.RegisterAgent(context.Background(), app.RegisterAgentRequest{Name: "a07-secret", Role: model.RoleDeveloper})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.ImportTasks(context.Background(), []model.Task{{ID: "TASK-A07-SECRET", Title: "secret output", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Run(context.Background(), app.RunRequest{TaskID: "TASK-A07-SECRET", AgentID: agent.ID, Adapter: "secret"}); !errors.Is(err, evidence.ErrSecretRejected) {
		t.Fatalf("Run error = %v, want secret rejection", err)
	}

	scan := func(root string) error {
		return filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil || info == nil || info.IsDir() {
				return walkErr
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(data), a07ProviderSecretMarker) {
				t.Fatalf("provider secret marker persisted in artifact/file %s", path)
			}
			return nil
		})
	}
	if err := scan(filepath.Join(repo.Path(), ".marshal")); err != nil {
		t.Fatal(err)
	}
	events, err := rt.Events(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if encoded, err := json.Marshal(events); err != nil {
		t.Fatal(err)
	} else if strings.Contains(string(encoded), a07ProviderSecretMarker) {
		t.Fatal("provider secret marker persisted in audit events")
	}
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	if err := scan(filepath.Join(repo.Path(), ".marshal")); err != nil {
		t.Fatal(err)
	}
}
