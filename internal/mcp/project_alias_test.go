package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/model"
)

// A role-bound server accepts the project's durable identity and root as
// names of the bound project; any other project stays a policy denial.
func TestRoleServerMapsBoundProjectAliasesToStoreID(t *testing.T) {
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
	project, err := runtime.Store().Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(runtime)
	srv.role = "marshal"
	caller := auth.Principal{ID: "marshal"}
	for _, alias := range []string{project.ID, runtime.ProjectIdentity(), runtime.ProjectRoot()} {
		out, err := srv.callTool(ctx, caller, "memory_status", map[string]any{"project_id": alias})
		if err != nil {
			t.Fatalf("%s: %v", alias, err)
		}
		var status struct {
			ProjectID string `json:"project_id"`
		}
		if err := json.Unmarshal([]byte(out), &status); err != nil || status.ProjectID != project.ID {
			t.Fatalf("%s: status %s %v", alias, out, err)
		}
	}
	if _, err := srv.callTool(ctx, caller, "memory_status", map[string]any{"project_id": "PROJECT-foreign"}); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("foreign project: %v", err)
	}
}
