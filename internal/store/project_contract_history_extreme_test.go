//go:build !governed

package store

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/projectcontext"
)

func TestProjectContractHistoryKeepsAcceptancesWithoutCommits(t *testing.T) {
	s, c, audience, now := acceptedContextStore(t)
	baseDigest, _ := c.CanonicalDigest()
	c.Revision++
	c.SupersedesDigest, c.RevisionReason = baseDigest, "Change storage responsibility"
	c.Components[0].Responsibility = "Preserve canonical state and revision history"
	req := admitContextDecision(t, s, c, true, "second-history")
	if _, err := s.CommitMCPProjectContractDecision(t.Context(), req, true, audience, now.Add(time.Minute), contextAudit(req, now)); err != nil {
		t.Fatal(err)
	}
	list, err := s.ProjectContractRevisions(t.Context(), c.Project)
	if err != nil || len(list) != 2 {
		t.Fatalf("history: %+v %v", list, err)
	}
	if list[0].Revision != 2 || list[1].Revision != 1 || list[0].Date != now.Add(time.Minute).Format(time.RFC3339Nano) || list[0].AcceptedBy != "owner" || list[0].Contract.Components[0].Responsibility != c.Components[0].Responsibility {
		t.Fatalf("acceptance metadata: %+v", list)
	}
	if list[0].Goal != "" || list[0].Run != "" {
		t.Fatal("history invented a goal or run")
	}
	foreign := c.Project
	foreign.CanonicalID = "other-project"
	if list, err := s.ProjectContractRevisions(t.Context(), foreign); err != nil || len(list) != 0 {
		t.Fatalf("cross-project history: %+v %v", list, err)
	}
}

func TestContractHistoryUsesGoalAtAcceptance(t *testing.T) {
	s := projectStore(t)
	if err := s.InitMCPProposals(t.Context()); err != nil {
		t.Fatal(err)
	}
	c, audience, _ := contextStoreDraft(t, s, nil)
	goal := intakeGoal()
	goal.SessionID, goal.ProjectID = "history-session", c.Project.CanonicalID
	goal.CreatedAt = time.Now().UTC().Add(-time.Minute)
	if err := s.SaveGoalContract(t.Context(), goal, 0); err != nil {
		t.Fatal(err)
	}
	req := admitContextDecision(t, s, c, true, "history-goal")
	at := time.Now().UTC().Add(time.Second)
	if _, _, err := s.RecordProjectContractInstruction(t.Context(), c.Project, goal.SessionID, "launch", "browser:history", BrowserAcceptanceInstruction, at); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitMCPProjectContractDecisionOnInstruction(t.Context(), req, true, audience, at, contextAudit(req, at)); err != nil {
		t.Fatal(err)
	}
	// A later revision in the same session must not replace the historical
	// goal lookup or make a current run appear to have accepted this version.
	goal.Revision = 2
	goal.CreatedAt = at.Add(time.Minute)
	goal.DesiredOutcome = "A later goal revision"
	if err := s.SaveGoalContract(t.Context(), goal, 1); err != nil {
		t.Fatal(err)
	}
	list, err := s.ProjectContractRevisions(t.Context(), c.Project)
	if err != nil || len(list) != 1 || list[0].Goal != goal.ID || list[0].Run != "" {
		t.Fatalf("goal at acceptance: %+v %v", list, err)
	}
}

func TestContractAdmissionRefusesDroppedComponent(t *testing.T) {
	s, c, _, _ := acceptedContextStore(t)
	baseDigest, _ := c.CanonicalDigest()
	c.Revision++
	c.SupersedesDigest, c.RevisionReason = baseDigest, "Replace storage with UI"
	c.Components = []projectcontext.Component{{ID: "ui", Responsibility: "Display state", Paths: []string{"internal/ui"}}}
	_, _, err := s.AdmitMCPProjectContract(t.Context(), MCPProposal{ID: "mcp:drop", Launch: "launch", Owner: "owner", Provider: "codex", Submission: "drop"}, ProjectContractSubmission{Action: "project-contract", ExpectedVersion: 1, Contract: c})
	if err == nil || !strings.Contains(err.Error(), "store") || !strings.Contains(err.Error(), "removed:true") {
		t.Fatalf("dropped component: %v", err)
	}
}
