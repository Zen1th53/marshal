//go:build !governed

package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
)

func menuPausedRuns(t *testing.T) (*Workspace, model.GoalContract) {
	t.Helper()
	ws, rt, goal := menuGoalFixture(t, "SESSION-menu-resume", model.ConfirmationApproved)
	store := rt.Execution().Engine().RunStore()
	now := time.Now().UTC()
	for _, run := range []execution.ExecutionRun{
		{RunID: "RUN-menu-chosen", SessionID: ws.sessionID, ProjectID: projectid.ID(rt.ProjectIdentity()), GoalID: goal.ID, GoalRevision: goal.Revision, State: execution.RunPaused, Version: 1, StartedAt: now, UpdatedAt: now},
		{RunID: "RUN-menu-sibling", SessionID: "SESSION-other", ProjectID: projectid.ID(rt.ProjectIdentity()), GoalID: goal.ID, GoalRevision: goal.Revision, State: execution.RunPaused, Version: 1, StartedAt: now, UpdatedAt: now},
	} {
		if err := store.CreateRun(t.Context(), run); err != nil {
			t.Fatal(err)
		}
	}
	return ws, goal
}

func TestExtremeMenuResumeExactPausedSessionRun(t *testing.T) {
	ws, _ := menuPausedRuns(t)
	source := ws.controlSource()
	b := source.Bindings()["CTUI-0073"]
	if !b.Bound() || len(b.Inputs) != 1 {
		t.Fatal("typed canonical run continuation missing")
	}
	c := &Confirmation{}
	req := ActionRequest{Action: b.Action, ProjectID: source.ProjectID, SessionID: source.SessionID, Inputs: map[string]string{"run_id": "RUN-menu-chosen"}}
	if err := c.Begin(t.Context(), b, req); err != nil {
		t.Fatal(err)
	}
	before, _ := source.Authority.GetRun(t.Context(), "RUN-menu-chosen")
	if before.State != execution.RunPaused {
		t.Fatal("prepare resumed execution")
	}
	c.MoveSelection(1)
	out, err := c.Submit(t.Context())
	if err != nil || !out.Succeeded() || !strings.Contains(out.Detail, "native conversation") {
		t.Fatal(out, err)
	}
	proof, err := source.Authority.GetRun(t.Context(), "RUN-menu-chosen")
	if err != nil || proof.Version <= 1 || proof.State == execution.RunPaused {
		t.Fatal(proof, err)
	}
	sibling, err := source.Authority.GetRun(t.Context(), "RUN-menu-sibling")
	if err != nil || sibling.Version != 1 || sibling.State != execution.RunPaused {
		t.Fatal("sibling changed", sibling, err)
	}
}

func TestExtremeMenuSessionResumeRefusesForeignAndSupersededGoal(t *testing.T) {
	ws, goal := menuPausedRuns(t)
	source := ws.controlSource()
	b := source.Bindings()["CTUI-0073"]
	for _, id := range []string{"RUN-menu-sibling", "RUN-missing", ""} {
		c := &Confirmation{}
		if err := c.Begin(t.Context(), b, ActionRequest{Action: b.Action, ProjectID: source.ProjectID, SessionID: source.SessionID, Inputs: map[string]string{"run_id": id}}); err == nil {
			t.Fatal("foreign or missing run reached confirmation", id)
		}
	}
	c := &Confirmation{}
	req := ActionRequest{Action: b.Action, ProjectID: source.ProjectID, SessionID: source.SessionID, Inputs: map[string]string{"run_id": "RUN-menu-chosen"}}
	if err := c.Begin(t.Context(), b, req); err != nil {
		t.Fatal(err)
	}
	goal.Revision = 2
	goal.Confirmation = model.ConfirmationPending
	if err := ws.runtime.Store().SaveGoalContract(t.Context(), goal, 1); err != nil {
		t.Fatal(err)
	}
	c.MoveSelection(1)
	out, _ := c.Submit(t.Context())
	if out.Succeeded() {
		t.Fatal("superseded goal resumed")
	}
	proof, _ := source.Authority.GetRun(t.Context(), "RUN-menu-chosen")
	if proof.Version != 1 || proof.State != execution.RunPaused {
		t.Fatal("refusal mutated run", proof)
	}
}

func TestExtremeMenuSessionResumeRefusesForeignEnvelopeAndRevokedGrant(t *testing.T) {
	for _, scenario := range []string{"foreign-project", "foreign-session", "revoked"} {
		t.Run(scenario, func(t *testing.T) {
			ws, _ := menuPausedRuns(t)
			source := ws.controlSource()
			b := source.Bindings()["CTUI-0073"]
			c := &Confirmation{}
			if err := c.Begin(t.Context(), b, ActionRequest{Action: b.Action, ProjectID: source.ProjectID, SessionID: source.SessionID, Inputs: map[string]string{"run_id": "RUN-menu-chosen"}}); err != nil {
				t.Fatal(err)
			}
			req := c.Request()
			switch scenario {
			case "foreign-project":
				req.ProjectID = "PROJECT-foreign"
			case "foreign-session":
				req.SessionID = "SESSION-foreign"
			case "revoked":
				grants, err := ws.runtime.Store().ListCapabilityGrants(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, grant := range grants {
					for _, action := range grant.Scope.Actions {
						if action == "run.control" {
							if err := ws.runtime.Store().RevokeCapabilityGrant(t.Context(), grant.ID, time.Now().UTC()); err != nil {
								t.Fatal(err)
							}
							found = true
						}
					}
				}
				if !found {
					t.Fatal("no run control grant")
				}
			}
			out, err := b.Execute(t.Context(), req)
			if err == nil && out.Succeeded() {
				t.Fatal("unauthorized continuation succeeded", out)
			}
			proof, _ := source.Authority.GetRun(t.Context(), "RUN-menu-chosen")
			if proof.Version != 1 || proof.State != execution.RunPaused {
				t.Fatal("denial mutated run", proof)
			}
		})
	}
}
