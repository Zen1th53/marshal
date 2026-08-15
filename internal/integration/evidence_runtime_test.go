package integration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/model"
)

const a06SecretMarker = "MARSHAL_TEST_SECRET_T06_A06_provider_6f21"

type secretEvidenceAdapter struct{ fakeCommitAdapter }

func (secretEvidenceAdapter) Run(ctx context.Context, request adapter.Request) (adapter.Result, error) {
	result, err := (fakeCommitAdapter{}).Run(ctx, request)
	result.Stdout = []byte(a06SecretMarker)
	return result, err
}

func TestRuntimeRunRecordsCanonicalEvidenceAndAudit(t *testing.T) {
	repo := runtimeIntegrationRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	rt, err := app.OpenWithOptions(context.Background(), repo.Path(), app.Options{Adapters: map[string]adapter.Adapter{"fake": fakeCommitAdapter{}}})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	agent, err := rt.RegisterAgent(context.Background(), app.RegisterAgentRequest{Name: "evidence-runtime", Role: model.RoleDeveloper})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.ImportTasks(context.Background(), []model.Task{{ID: "TASK-A06-EVIDENCE", Title: "evidence runtime", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	result, err := rt.Run(context.Background(), app.RunRequest{TaskID: "TASK-A06-EVIDENCE", AgentID: agent.ID, Adapter: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"COMMAND", "OUTPUT", "ENV"} {
		if _, err := rt.Evidence(context.Background(), evidence.NodeID("EVIDENCE-RUN-"+result.RunID+"-"+suffix)); err != nil {
			t.Fatalf("%s evidence: %v", suffix, err)
		}
	}
	events, err := rt.Events(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var stored, linked int
	for _, event := range events {
		if event.Type == "evidence.node.stored" {
			stored++
		}
		if event.Type == "evidence.edge.linked" {
			linked++
		}
	}
	if stored != 3 || linked != 2 {
		t.Fatalf("evidence audit counts stored=%d linked=%d", stored, linked)
	}
}

func TestRuntimeProviderSecretIsDigestOnly(t *testing.T) {
	repo := runtimeIntegrationRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	rt, err := app.OpenWithOptions(context.Background(), repo.Path(), app.Options{Adapters: map[string]adapter.Adapter{"secret": secretEvidenceAdapter{}}})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	agent, err := rt.RegisterAgent(context.Background(), app.RegisterAgentRequest{Name: "secret-provider", Role: model.RoleDeveloper})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.ImportTasks(context.Background(), []model.Task{{ID: "TASK-A06-SECRET", Title: "secret evidence", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	result, err := rt.Run(context.Background(), app.RunRequest{TaskID: "TASK-A06-SECRET", AgentID: agent.ID, Adapter: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	events, err := rt.Events(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), a06SecretMarker) {
		t.Fatal("provider secret marker leaked into audit events")
	}
	node, err := rt.Evidence(context.Background(), evidence.NodeID("EVIDENCE-RUN-"+result.RunID+"-OUTPUT"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(node.Metadata["stdout_digest"], a06SecretMarker) {
		t.Fatal("provider secret marker leaked into evidence metadata")
	}
}
