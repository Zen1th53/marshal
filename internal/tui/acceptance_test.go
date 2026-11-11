package tui

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

func acceptanceWorkspace(t *testing.T) (*store.Store, *Workspace, context.Context) {
	t.Helper()
	ctx := context.Background()
	repo := testgit.New(t)
	if _, err := app.Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatalf("bootstrap acceptance runtime: %v", err)
	}
	runtime, err := app.Open(ctx, repo.Path())
	if err != nil {
		t.Fatalf("open acceptance runtime: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	binding, found := projectid.LoadBinding(filepath.Join(repo.Path(), projectid.StateDirName))
	if !found {
		t.Fatal("bootstrapped acceptance project has no durable project identity")
	}
	st := runtime.Store()
	ws := NewWorkspace(st, runtime.ProjectID(), "ACCEPTANCE-session")
	ws.workDir = repo.Path()
	ws.tmuxSession = fixtureTmuxNamespace(t, repo.Path())
	t.Cleanup(ws.Close)
	ws.AttachRuntime(runtime, binding.ID)
	return st, ws, ctx
}

func acceptanceGoal(ws *Workspace) model.GoalContract {
	now := time.Now().UTC()
	return model.GoalContract{ID: "GOAL-acceptance", SessionID: ws.sessionID, ProjectID: ws.projectID,
		OriginalRequest: "exercise the acceptance lifecycle", RequestDigest: "sha256:acceptance-goal-v1",
		ConstitutionVersion: constitution.Current.String(), Confirmation: model.ConfirmationPending, Revision: 1,
		DesiredOutcome: "Exercise durable Community TUI acceptance paths", Risk: model.R1,
		AuthoritySource: "acceptance fixture", UnderstandingState: model.GoalReady, CreatedAt: now, UpdatedAt: now}
}
