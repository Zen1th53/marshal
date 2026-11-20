//go:build linux && !governed

package tui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestProjectContractSubmitQualityRefusesBeforeOwnerReview(t *testing.T) {
	w, _, cfg := liveMCPFixture(t)
	criteria := []string{"Review shows connection labels", "All 18 fixes pass tests", "Stored state round-trips"}
	goal := model.GoalContract{
		ID: "GOAL-quality", SessionID: w.sessionID, ProjectID: w.runtime.ProjectIdentity(),
		DesiredOutcome: "Fix architecture review", SuccessCriteria: criteria,
		Scope: []string{"Architecture review packages"}, Constraints: []model.Constraint{{Text: "Use the standard library"}},
		DoNotDo: []string{"Preserve accepted history"}, Confirmation: model.ConfirmationApproved,
		Risk: model.R0, AuthoritySource: "operator",
	}
	if err := w.store.SaveGoalContract(t.Context(), goal, 0); err != nil {
		t.Fatal(err)
	}
	confirmed := [8]string{"Review fixes only", "", "", "The owner can inspect the result", "go vet passes"}
	w.req.confirmed = &confirmed
	coverage := append(append([]string(nil), criteria...), goal.Scope[0], goal.Constraints[0].Text, goal.DoNotDo[0], confirmed[0], confirmed[3], confirmed[4])
	child := memoryChild(t, w.runtime.ProjectRoot(), &cfg)
	var inventory store.ProjectContractInventoryPage
	if err := json.Unmarshal([]byte(child.call(t, "marshal_project_contract_inventory", map[string]any{}, true)), &inventory); err != nil {
		t.Fatal(err)
	}
	draft := func() store.ProjectContractSubmission {
		return store.ProjectContractSubmission{Action: "project-contract", Contract: projectcontext.Contract{
			FormatVersion: projectcontext.FormatVersion, Project: w.runtime.ProjectContractBinding(), ID: "review-fixes", Revision: 1,
			Purpose: "Fix architecture review", Invariants: []string{"Keep accepted history"}, ReviewedInventoryDigest: inventory.CompletenessDigest,
			Components: []projectcontext.Component{
				{ID: "architecturereview", Responsibility: "Serve the review page", Paths: []string{"internal/architecturereview"}, Dependencies: []string{"architecturecodec"}, Covers: append([]string(nil), coverage...)},
				{ID: "architecturecodec", Responsibility: "Encode review state", Paths: []string{"internal/architecturecodec"}},
			},
			Connections: []projectcontext.Connection{{From: "architecturereview", To: "architecturecodec", Carries: "review state and layout JSON"}},
		}}
	}
	for _, tc := range []struct {
		name   string
		mutate func(*projectcontext.Contract)
		want   []string
	}{
		{"missing-connection", func(c *projectcontext.Contract) { c.Connections = nil }, []string{"architecturereview", "architecturecodec", "carries", "contract.connections"}},
		{"blank-carries", func(c *projectcontext.Contract) { c.Connections[0].Carries = " \t" }, []string{"does not say what it carries"}},
		{"missing-responsibility", func(c *projectcontext.Contract) { c.Components[1].Responsibility = " \n" }, []string{"architecturecodec", "has no responsibility"}},
		{"uncovered-criteria", func(c *projectcontext.Contract) { c.Components[0].Covers = c.Components[0].Covers[3:] }, criteria},
		{"uncovered-confirmed-goal", func(c *projectcontext.Contract) {
			c.Components[0].Covers = append(append([]string(nil), criteria...), confirmed[0], confirmed[3], confirmed[4])
		}, []string{goal.Scope[0], goal.Constraints[0].Text, goal.DoNotDo[0]}},
		{"uncovered-confirmed-card", func(c *projectcontext.Contract) { c.Components[0].Covers = c.Components[0].Covers[:6] }, []string{confirmed[0], confirmed[3], confirmed[4]}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sub := draft()
			tc.mutate(&sub.Contract)
			msg := child.call(t, "marshal_project_contract_submit", map[string]any{"submission_id": tc.name, "proposal": sub}, false)
			for _, want := range tc.want {
				if !strings.Contains(msg, want) {
					t.Errorf("refusal %q lacks %q", msg, want)
				}
			}
			pending, err := w.store.PendingMarshalProposals(t.Context(), w.projectID)
			if err != nil || len(pending) != 0 || !w.permissions.queue.Empty() {
				t.Fatalf("invalid draft reached owner review: %+v, %v", pending, err)
			}
		})
	}
	// Two components can cover all criteria; submission stays pending owner review.
	var receipt projectContractMCPReceipt
	if err := json.Unmarshal([]byte(child.call(t, "marshal_project_contract_submit", map[string]any{"submission_id": "good-quality", "proposal": draft()}, true)), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Accepted || receipt.State != "pending_permission" {
		t.Fatalf("quality pass bypassed owner review: %+v", receipt)
	}
}

func TestContractRequirementsIncludeConfirmedCardWithoutGoal(t *testing.T) {
	w := &Workspace{}
	confirmed := [8]string{"Limit scope", "", "", "Print hello", "Keep tests passing"}
	w.req.confirmed = &confirmed
	got, err := w.activeContractRequirements(t.Context())
	if err != nil || strings.Join(got, "|") != "Limit scope|Print hello|Keep tests passing" {
		t.Fatalf("confirmed card requirements = %q, %v", got, err)
	}
}

func TestContractRequirementsReadFailureDoesNotSkipCoverage(t *testing.T) {
	w, _, _ := liveMCPFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := w.activeContractRequirements(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("requirement read failure ignored: %v", err)
	}
}
