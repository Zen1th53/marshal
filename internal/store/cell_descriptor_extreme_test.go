//go:build !governed

package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/cell"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
)

func privateCellFixture(t *testing.T) (*Store, string, cell.ApprovedCheckDescriptor, CommandRecord) {
	t.Helper()
	s, path, _, _ := memorySnapshotFixture(t)
	goal := model.GoalContract{ID: "GOAL-cell", SessionID: "SESSION-cell", ProjectID: "project", Revision: 1, DesiredOutcome: "check", ExpectedArtifact: "result", SuccessCriteria: []string{"check passes"}, Risk: model.R1, AuthoritySource: "operator", Confirmation: model.ConfirmationApproved}
	if err := s.SaveGoalContract(t.Context(), goal, 0); err != nil {
		t.Fatal(err)
	}
	p := plan.ExecutionPlan{ID: "PLAN-cell", ProjectID: projectid.ID("PROJECT-0123456789abcdef0123456789abcdef"), Goal: plan.BindGoal(goal), Version: 1, State: plan.StateApproved, Mode: plan.ModeStandard, Tasks: []plan.Task{{ID: "check", Title: "check"}}, Checks: map[string][]string{"check": {"true"}}}
	goal.ProjectID = string(p.ProjectID)
	goal.Revision = 2
	if err := s.SaveGoalContract(t.Context(), goal, 1); err != nil {
		t.Fatal(err)
	}
	p.Goal = plan.BindGoal(goal)
	p.ApprovedScope = &plan.ApprovalScope{Goal: p.Goal, Mode: p.Mode, Tasks: p.Tasks, Checks: p.Checks}
	p.ApprovalScopeDigest = p.ApprovedScope.Digest()
	if err := s.SavePlan(t.Context(), p, 0); err != nil {
		t.Fatal(err)
	}
	workspace := "/tmp/cell-fixture"
	taskSum := sha256.Sum256([]byte(p.ID + "\x00" + "1" + "\x00check"))
	task := model.Task{ID: "TASK-P05-" + hex.EncodeToString(taskSum[:12]), Title: "check", Status: model.TaskWorking, Risk: model.R1, Revision: 1, Worktree: &workspace}
	b := TaskBinding{SessionID: goal.SessionID, GoalID: goal.ID, GoalRevision: goal.Revision, PlanID: p.ID, PlanVersion: p.Version}
	if _, err := s.ImportTasks(WithTaskBinding(t.Context(), b), []model.Task{task}); err != nil {
		t.Fatal(err)
	}
	spec := cell.Spec{TaskID: cell.TaskID(task.ID), Workspace: workspace, Backend: cell.BackendBubblewrap, NetworkProfile: "deny-by-default"}
	id, err := cell.CellIdentity(spec)
	if err != nil {
		t.Fatal(err)
	}
	checkSum := sha256.Sum256([]byte("true"))
	d := cell.ApprovedCheckDescriptor{ID: id, ProjectID: string(p.ProjectID), SessionID: goal.SessionID, TaskID: task.ID, TaskRevision: 1, GoalID: goal.ID, GoalRevision: goal.Revision, PlanID: p.ID, PlanVersion: 1, PlanTaskID: "check", CheckIndex: 0, CheckDigest: hex.EncodeToString(checkSum[:]), ApprovalScopeDigest: p.ApprovalScopeDigest, Spec: spec, Revision: 1, State: cell.StateReady}
	grant := capability.Grant{ID: "cap-cell", Subject: "operator", TaskID: capability.TaskID(d.ProjectID), Kind: capability.KindFilesystemWrite, Scope: capability.Scope{Resource: path, Actions: []string{"cell.prepare", "cell.exec", "cell.destroy"}}, IssuedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour), Issuer: "operator", IdempotencyKey: "cell-fixture"}
	if err := s.PutCapabilityGrant(t.Context(), grant); err != nil {
		t.Fatal(err)
	}
	rec := CommandRecord{ProjectID: d.ProjectID, SessionID: d.SessionID, Actor: "operator", TargetID: "cell:" + string(id), Operation: "cell.prepare", Key: "prepare", Digest: "prepare-digest", CapabilityGrantID: string(grant.ID)}
	return s, path, d, rec
}

func TestPrivateCellDescriptorAtomicReceiptCASAndRestart(t *testing.T) {
	s, path, d, rec := privateCellFixture(t)
	if err := s.CommitApprovedCell(t.Context(), rec, d, 0, false); err != nil {
		t.Fatal(err)
	}
	saved, err := s.GetApprovedCell(t.Context(), d.ProjectID, d.SessionID, d.ID, 1)
	if err != nil || saved.State != cell.StateReady {
		t.Fatalf("descriptor %+v %v", saved, err)
	}
	canonical, err := s.GetCell(t.Context(), d.ID)
	if err != nil || canonical.State != d.State {
		t.Fatalf("canonical cell %+v %v", canonical, err)
	}
	rec.Operation = "cell.exec"
	rec.Key = "exec"
	rec.Digest = "exec-digest"
	rec.ExpectedVersion = 1
	d.Revision = 2
	d.State = cell.StateRunning
	if err := s.CommitApprovedCell(t.Context(), rec, d, 1, false); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitApprovedCell(t.Context(), rec, d, 1, false); err == nil {
		t.Fatal("duplicate claim started again")
	}
	d.Revision = 3
	d.State = cell.StateDestroyed
	d.Settled = true
	code := 0
	d.ExitCode = &code
	if err := s.CommitApprovedCell(t.Context(), rec, d, 2, true); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	version, found, err := s.ApprovedCellCommandResult(t.Context(), rec)
	if err != nil || !found || version != 3 {
		t.Fatalf("receipt %d %v %v", version, found, err)
	}
	retained, err := s.GetApprovedCell(t.Context(), d.ProjectID, d.SessionID, d.ID, version)
	if err != nil || !retained.Settled || retained.ExitCode == nil || *retained.ExitCode != 0 {
		t.Fatalf("durable settlement %+v %v", retained, err)
	}
	if _, err := s.GetApprovedCell(t.Context(), d.ProjectID, "foreign-session", d.ID, 0); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("foreign session: %v", err)
	}
}

func TestPrivateCellCommitRefusesRevokedGrantAndStaleScope(t *testing.T) {
	for _, kind := range []string{"revoked", "task-revision", "foreign-session", "wrong-action"} {
		t.Run(kind, func(t *testing.T) {
			s, _, d, rec := privateCellFixture(t)
			switch kind {
			case "revoked":
				if err := s.RevokeCapabilityGrant(t.Context(), capability.GrantID(rec.CapabilityGrantID), time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
			case "task-revision":
				d.TaskRevision++
			case "foreign-session":
				d.SessionID = "foreign-session"
				rec.SessionID = d.SessionID
			case "wrong-action":
				rec.Operation = "task.control"
			}
			if err := s.CommitApprovedCell(t.Context(), rec, d, 0, false); err == nil {
				t.Fatal("unadmitted descriptor committed")
			}
			if _, err := s.GetCell(t.Context(), d.ID); !errors.Is(err, model.ErrNotFound) {
				t.Fatalf("partial canonical cell persisted: %v", err)
			}
			if _, found, err := s.CommandResult(t.Context(), rec); err != nil || found {
				t.Fatalf("failed write retained receipt: %v %v", found, err)
			}
			if kind == "revoked" {
				if err := s.CommitApprovedCell(t.Context(), rec, d, 0, false); !errors.Is(err, authz.ErrDenied) {
					t.Fatalf("revocation: %v", err)
				}
			}
		})
	}
}

func TestPrivateCellOperationCannotBorrowAnotherLifecyclePhase(t *testing.T) {
	for _, operation := range []string{"cell.exec", "cell.destroy"} {
		t.Run("initial-"+operation, func(t *testing.T) {
			s, _, d, rec := privateCellFixture(t)
			rec.Operation = operation
			if err := s.CommitApprovedCell(t.Context(), rec, d, 0, false); err == nil {
				t.Fatal("non-prepare operation created descriptor")
			}
		})
	}
	for _, operation := range []string{"cell.prepare", "cell.destroy"} {
		t.Run("run-"+operation, func(t *testing.T) {
			s, _, d, rec := privateCellFixture(t)
			if err := s.CommitApprovedCell(t.Context(), rec, d, 0, false); err != nil {
				t.Fatal(err)
			}
			rec.Key = "wrong-phase"
			rec.Operation = operation
			rec.ExpectedVersion = 1
			d.Revision = 2
			d.State = cell.StateRunning
			if err := s.CommitApprovedCell(t.Context(), rec, d, 1, false); err == nil {
				t.Fatal("non-exec operation started descriptor")
			}
		})
	}
	t.Run("cross-task-approved-check", func(t *testing.T) {
		s, _, d, rec := privateCellFixture(t)
		d.PlanTaskID = "another-check"
		if err := s.CommitApprovedCell(t.Context(), rec, d, 0, false); err == nil {
			t.Fatal("descriptor substituted another plan task")
		}
	})
}
