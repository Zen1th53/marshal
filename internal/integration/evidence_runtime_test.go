package integration

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/model"
)

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
