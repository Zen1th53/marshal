package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestRunDigestExposesPauseFields(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	srv := NewServer(runtime)
	listed := false
	for _, tool := range srv.listTools() {
		listed = listed || tool.Name == "run_digest"
	}
	if !listed {
		t.Fatal("run_digest is not offered to the Marshal")
	}
	caller := auth.Principal{ID: "mcp-test"}
	var empty runDigest
	raw, err := srv.callTool(ctx, caller, "run_digest", nil)
	if err != nil || json.Unmarshal([]byte(raw), &empty) != nil || !empty.NoRun || empty.Tasks == nil {
		t.Fatalf("digest without a run: %s %v", raw, err)
	}
	service := runtime.Marshal()
	if service == nil || service.ProjectID == "" {
		t.Skip("no Marshal service in this edition")
	}
	run := marshal.Run{PlanID: "plan", PlanVersion: 2, Settings: marshal.DefaultSettings(), State: marshal.AwaitingUser, Pause: &marshal.Pause{Reason: "waiting", Code: marshal.CodeBudget, Action: marshal.PauseResume, TaskID: "fix-add"}, Tasks: []marshal.Task{{PlanTaskID: "fix-add", Title: "Fix add", Worker: "codex", State: marshal.Merged}}}
	if _, err := service.Store.SetMarshalRun(ctx, service.ProjectID, "RUN-digest", run, 0); err != nil {
		t.Fatal(err)
	}
	var got runDigest
	raw, err = srv.callTool(ctx, caller, "run_digest", nil)
	if err != nil || json.Unmarshal([]byte(raw), &got) != nil {
		t.Fatalf("digest: %s %v", raw, err)
	}
	if got.NoRun || got.RunID != "RUN-digest" || got.State != marshal.AwaitingUser || got.PlanVersion != 2 || len(got.Tasks) != 1 || got.Tasks[0].ID != "fix-add" || got.Tasks[0].Worker != "codex" || got.Tasks[0].State != marshal.Merged || got.CloseAuthorized || got.PauseCode != marshal.CodeBudget || got.PauseAction != string(marshal.PauseResume) || got.PauseTask != "fix-add" {
		t.Fatalf("wrong digest %+v", got)
	}
}

func TestRunDigestReadsTheCurrentRunOnce(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	srv := NewServer(runtime)
	listed := false
	for _, tool := range srv.listTools() {
		listed = listed || tool.Name == "run_digest"
	}
	if !listed {
		t.Fatal("run_digest is not offered to the Marshal")
	}
	caller := auth.Principal{ID: "mcp-test"}
	var empty runDigest
	raw, err := srv.callTool(ctx, caller, "run_digest", nil)
	if err != nil || json.Unmarshal([]byte(raw), &empty) != nil || !empty.NoRun || empty.Tasks == nil {
		t.Fatalf("digest without a run: %s %v", raw, err)
	}
	service := runtime.Marshal()
	if service == nil || service.ProjectID == "" {
		t.Skip("no Marshal service in this edition")
	}
	run := marshal.Run{PlanID: "plan", PlanVersion: 2, Settings: marshal.DefaultSettings(), State: marshal.Verifying, Tasks: []marshal.Task{{PlanTaskID: "fix-add", Title: "Fix add", Worker: "codex", State: marshal.Merged}}}
	if _, err := service.Store.SetMarshalRun(ctx, service.ProjectID, "RUN-digest", run, 0); err != nil {
		t.Fatal(err)
	}
	var got runDigest
	raw, err = srv.callTool(ctx, caller, "run_digest", nil)
	if err != nil || json.Unmarshal([]byte(raw), &got) != nil {
		t.Fatalf("digest: %s %v", raw, err)
	}
	if got.NoRun || got.RunID != "RUN-digest" || got.State != marshal.Verifying || got.PlanVersion != 2 || len(got.Tasks) != 1 || got.Tasks[0].ID != "fix-add" || got.Tasks[0].Worker != "codex" || got.Tasks[0].State != marshal.Merged || got.CloseAuthorized {
		t.Fatalf("wrong digest %+v", got)
	}
}
