//go:build !governed

package app

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestMarshalGoalHandoffIncludesAcceptedArchitectureContract(t *testing.T) {
	r, b, _, _ := requiredContextFixture(t, true)
	h, ok, err := r.MarshalGoalHandoff(t.Context(), b.Session)
	if err != nil || !ok {
		t.Fatalf("MarshalGoalHandoff: ok=%v err=%v", ok, err)
	}
	if !h.ArchitectureAccepted {
		t.Fatalf("expected ArchitectureAccepted=true, got %+v", h)
	}
	if !strings.Contains(h.Phase, "architecture accepted") {
		t.Fatalf("expected architecture-accepted phase, got %q", h.Phase)
	}
	formatted := FormatMarshalHandoff(h)
	if !strings.Contains(formatted, b.GoalID) || !strings.Contains(formatted, b.Contract.ID) || !strings.Contains(formatted, "accepted (") {
		t.Fatalf("handoff missing goal or accepted architecture contract:\n%s", formatted)
	}
}

func TestNativePlanningBootstrapNamesActiveGoalWhenArchitecturePending(t *testing.T) {
	r := openNetpolRuntime(t)
	ctx := t.Context()
	control, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ownerCtx := control.Context(ctx)
	now := time.Now().UTC()
	goal := model.GoalContract{
		ID:              "GOAL-active-no-arch",
		SessionID:       "SESSION-active-no-arch",
		Revision:        1,
		DesiredOutcome:  "Keep this active goal across provider switch",
		Risk:            model.R1,
		AuthoritySource: "operator:test",
		Confirmation:    model.ConfirmationApproved,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := r.Store().SaveGoalContract(ownerCtx, goal, 0); err != nil {
		t.Fatalf("save goal: %v", err)
	}
	principal := authz.Principal{ID: "owner", Role: authz.Role{Name: "marshal", Authorities: []authz.Authority{authz.AuthorityPolicyAdmin}}}
	view, token, err := r.PrepareNativePlanningRead(ownerCtx, principal, goal.SessionID, 16384)
	if err != nil || !view.Bootstrap || token != nil || view.Qualified {
		t.Fatalf("unexpected read result: view=%+v token=%v err=%v", view, token, err)
	}
	if view.GoalID != goal.ID || view.GoalRevision != goal.Revision {
		t.Fatalf("bootstrap lost active goal binding: got %s rev %d", view.GoalID, view.GoalRevision)
	}
	if !strings.Contains(view.Context, "INTAKE ONLY") || !strings.Contains(view.Context, goal.ID) || !strings.Contains(view.Context, "do not record a new one") {
		t.Fatalf("bootstrap context did not name active goal or warn against creating a new one:\n%s", view.Context)
	}
}
