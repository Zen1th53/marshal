//go:build !governed

package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

// TestGoalCancelKeepsPendingOnlyOverride proves the shipped build still applies
// CTUI-0048's "only pending goals can be cancelled here" override after the
// navigation menu was deleted: Bindings() routes it through
// configureBuildBindings → configureGoalControlBindings.
func TestGoalCancelKeepsPendingOnlyOverride(t *testing.T) {
	goal := model.GoalContract{ID: "GOAL-confirmed", SessionID: "s", ProjectID: "p",
		Revision: 3, Confirmation: model.ConfirmationApproved, RequestDigest: "digest-confirmed"}
	auth := &unprovenGoalMenuAuthority{fakeAuthority: &fakeAuthority{goal: goal}}
	source := &ControlSource{Authority: auth, SessionID: "s", ProjectID: "p"}

	binding := source.Bindings()["CTUI-0048"]
	if binding.Prepare == nil {
		t.Fatal("CTUI-0048 has no Prepare after configureBuildBindings")
	}
	if _, err := binding.Prepare(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "only pending goals can be cancelled here") {
		t.Fatalf("confirmed goal cancellation = %v, want the pending-only refusal", err)
	}
}
