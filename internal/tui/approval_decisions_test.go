package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
)

func TestComposerTypedApprovalAmbiguityAndOwnerDecider(t *testing.T) {
	session := "SESSION-composer-approval"
	_, runtime := realControlWorkspace(t, session)
	ws := NewWorkspace(runtime.Store(), runtime.ProjectIdentity(), session)
	ws.AttachRuntime(runtime, projectid.ID(runtime.ProjectIdentity()))
	ctx := context.Background()
	if _, err := ws.ExecuteCommand(ctx, "/goal create Fix the parser typo."); err != nil {
		t.Fatal(err)
	}
	goal, err := runtime.Store().GetActiveGoalContract(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Store().CreateApproval(ctx, model.Approval{ID: goal.ID, ProjectID: runtime.ProjectID(), Operation: model.DestructiveOperation, Scope: "project", Target: "file", RequestedBy: "worker", Status: model.ApprovalRequested, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.ExecuteCommand(ctx, "/approve "+goal.ID); err == nil || !strings.Contains(err.Error(), "goal:"+goal.ID+"@1") || !strings.Contains(err.Error(), "approval:"+goal.ID) {
		t.Fatalf("ambiguity: %v", err)
	}
	pending, err := runtime.Store().GetApproval(ctx, goal.ID)
	if err != nil || pending.Status != model.ApprovalRequested {
		t.Fatalf("ambiguous command mutated: %+v %v", pending, err)
	}
	authority := ws.controlSource().Authority.(*runtimeControlAuthority)
	if err := authority.DecideApproval(ctx, "goal-confirm:"+goal.ID, true, "planning-model-spoof", "owner confirmed interpretation"); err != nil {
		t.Fatal(err)
	}
	confirmed, err := runtime.Store().GetActiveGoalContract(ctx, session)
	if err != nil || confirmed.Confirmation != model.ConfirmationApproved {
		t.Fatalf("confirmation: %+v %v", confirmed, err)
	}
	line := fmt.Sprintf("/reject approval:%s owner declined action", goal.ID)
	if out, err := ws.ExecuteCommand(ctx, line); err != nil || !strings.Contains(out, "denied") {
		t.Fatalf("reject: %s %v", out, err)
	}
	resolved, err := runtime.Store().GetApproval(ctx, goal.ID)
	if err != nil {
		t.Fatal(err)
	}
	owner, _ := auth.LocalFromContext(authority.localControl.Context(ctx))
	if resolved.ApprovedBy != owner.ID() || resolved.ApprovedBy == "planning-model-spoof" {
		t.Fatalf("decider: %+v", resolved)
	}
}
