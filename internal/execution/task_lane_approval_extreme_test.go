//go:build !governed

package execution

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExtremePendingTaskLaneRestartPreservesExactApprovalWait(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "expired"}[expired], func(t *testing.T) {
			e, parent, base := laneFixture(t)
			lane, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-1", base)
			if err != nil {
				t.Fatal(err)
			}
			task := lane.Tasks["task-1"]
			task.ApprovalRequired = true
			lane.Tasks["task-1"] = task
			if err := e.store.UpdateRun(t.Context(), *lane); err != nil {
				t.Fatal(err)
			}
			waiting, err := e.ExecuteTaskBound(t.Context(), lane.RunID, "task-1", base)
			if err != nil || waiting.State != RunNeedsApproval {
				t.Fatalf("initial wait: %+v %v", waiting, err)
			}
			id := waiting.Tasks["task-1"].ApprovalID
			receipt, err := e.ApprovalManager().GetApproval(id)
			if err != nil {
				t.Fatal(err)
			}
			if expired {
				if _, err := e.ApprovalManager().Decide(id, true, "fixture-clock", "expired test", receipt.ExpiresAt.Add(time.Second)); err == nil {
					t.Fatal("expired approval granted")
				}
				receipt, err = e.ApprovalManager().GetApproval(id)
				if err != nil || receipt.Status != ApprovalExpired {
					t.Fatalf("expiry: %+v %v", receipt, err)
				}
			}
			store, err := NewFileRunStore(e.store.(*FileRunStore).baseDir)
			if err != nil {
				t.Fatal(err)
			}
			fresh, err := NewEngineWithReaders(e.cfg, store, nil, e.planReader, e.goalReader)
			if err != nil {
				t.Fatal(err)
			}
			approvalPath := filepath.Join(e.approvals.dir, id+".json")
			beforeBytes, err := os.ReadFile(approvalPath)
			if err != nil {
				t.Fatal(err)
			}
			repeated, err := fresh.ExecuteTaskBound(t.Context(), lane.RunID, "task-1", base)
			if expired {
				// Owner rule (Extreme): an approval that expired unanswered
				// never stops the lane. Re-entry requests a fresh approval
				// without running a worker and leaves the expired one intact.
				renewed := repeated.Tasks["task-1"]
				if err != nil || repeated.State != RunNeedsApproval || renewed.State != TaskNeedsApproval || renewed.ApprovalID == "" || renewed.ApprovalID == id || renewed.Attempts != 0 {
					t.Fatalf("expired approval not renewed: %+v %v", repeated, err)
				}
				fresh2, err := fresh.ApprovalManager().GetApproval(renewed.ApprovalID)
				if err != nil || fresh2.Status != ApprovalRequested || fresh2.OperationType != "TASK_EXECUTE" || fresh2.ExpiresAt == nil || !time.Now().Before(*fresh2.ExpiresAt) {
					t.Fatalf("fresh approval: %+v %v", fresh2, err)
				}
				if afterBytes, err := os.ReadFile(approvalPath); err != nil || !bytes.Equal(beforeBytes, afterBytes) {
					t.Fatal("renewal changed the expired approval's bytes")
				}
				return
			}
			if err != nil || repeated.State != RunNeedsApproval || repeated.CurrentPhase != PhaseAwaitingApproval || repeated.Tasks["task-1"].ApprovalID != id || repeated.Tasks["task-1"].Attempts != 0 || repeated.Version != waiting.Version {
				t.Fatalf("restart invented execution or changed wait: %+v %v", repeated, err)
			}
			// Repair the real historical phantom-RUNNING shape without issuing
			// a replacement approval or dispatching any provider.
			repeated.State = RunRunning
			repeated.CurrentPhase = PhaseExecuting
			if err := store.UpdateRun(t.Context(), *repeated); err != nil {
				t.Fatal(err)
			}
			repaired, err := fresh.ExecuteTaskBound(t.Context(), lane.RunID, "task-1", base)
			if err != nil || repaired.State != RunNeedsApproval || repaired.Tasks["task-1"].ApprovalID != id || repaired.Tasks["task-1"].Attempts != 0 {
				t.Fatalf("phantom state repair: %+v %v", repaired, err)
			}
			after, err := fresh.ApprovalManager().GetApproval(id)
			if err != nil || after.Revision != receipt.Revision || after.Status != receipt.Status {
				t.Fatalf("restart renewed or changed approval: %+v %v", after, err)
			}
			afterBytes, err := os.ReadFile(approvalPath)
			if err != nil || !bytes.Equal(beforeBytes, afterBytes) {
				t.Fatal("restart changed exact approval expiry/binding bytes")
			}
		})
	}
}

func TestExtremeTaskLaneApprovalPreflightRefusesIncoherentBindings(t *testing.T) {
	for _, changed := range []string{"missing-id", "run", "task", "plan", "version", "approved", "consumed", "expired-requested-paused", "terminal", "paused", "blocked", "cancelling", "native-phantom"} {
		t.Run(changed, func(t *testing.T) {
			e, parent, base := laneFixture(t)
			lane, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-1", base)
			if err != nil {
				t.Fatal(err)
			}
			task := lane.Tasks["task-1"]
			task.ApprovalRequired = true
			lane.Tasks["task-1"] = task
			if err := e.store.UpdateRun(t.Context(), *lane); err != nil {
				t.Fatal(err)
			}
			waiting, err := e.ExecuteTaskBound(t.Context(), lane.RunID, "task-1", base)
			if err != nil {
				t.Fatal(err)
			}
			originalID := waiting.Tasks["task-1"].ApprovalID
			receipt, err := e.approvals.GetApproval(originalID)
			if err != nil {
				t.Fatal(err)
			}
			switch changed {
			case "missing-id":
				task = waiting.Tasks["task-1"]
				task.ApprovalID = ""
				waiting.Tasks["task-1"] = task
			case "terminal":
				waiting.State = RunCancelled
			case "paused":
				waiting.State = RunPaused
			case "blocked":
				waiting.State = RunBlocked
			case "cancelling":
				waiting.State = RunCancelling
			case "native-phantom":
				waiting.State, waiting.CurrentPhase = RunRunning, PhaseExecuting
				task = waiting.Tasks["task-1"]
				task.NativeTurn = &NativeTurnBinding{RunRevision: waiting.Version}
				waiting.Tasks["task-1"] = task
			case "approved", "consumed":
				if err := e.approvals.Approve(originalID, "fixture-owner", "fixture", time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
				if changed == "consumed" {
					if err := e.approvals.ValidateAndConsume(originalID, receipt.ActionDigest, receipt.StateDigest, time.Now().UTC()); err != nil {
						t.Fatal(err)
					}
				}
			default:
				r := ApprovalRequest{RunID: lane.RunID, TaskID: "task-1", PlanID: lane.PlanID, PlanVersion: lane.PlanVersion, OperationType: "TASK_EXECUTE"}
				switch changed {
				case "run":
					r.RunID = "foreign-run"
				case "task":
					r.TaskID = "foreign-task"
				case "plan":
					r.PlanID = "foreign-plan"
				case "version":
					r.PlanVersion++
				case "expired-requested-paused":
					// An expired, exactly bound approval is renewed only while
					// the lane really awaits it (owner rule, Extreme; see
					// TestExtremePendingTaskLaneRestartPreservesExactApprovalWait/expired).
					// A lane in any other state keeps its refusal.
					r.Now = time.Now().Add(-time.Hour)
					r.TTL = time.Second
					waiting.State = RunPaused
				}
				foreign, err := e.approvals.RequestApproval(r)
				if err != nil {
					t.Fatal(err)
				}
				task = waiting.Tasks["task-1"]
				task.ApprovalID = foreign.ApprovalID
				waiting.Tasks["task-1"] = task
			}
			if err := e.store.UpdateRun(t.Context(), *waiting); err != nil {
				t.Fatal(err)
			}
			before, err := e.store.GetRun(t.Context(), lane.RunID)
			if err != nil {
				t.Fatal(err)
			}
			approvalPath := filepath.Join(e.approvals.dir, originalID+".json")
			approvalBytes, err := os.ReadFile(approvalPath)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.ExecuteTaskBound(t.Context(), lane.RunID, "task-1", base); err == nil {
				t.Fatal("incoherent approval state admitted")
			}
			after, err := e.store.GetRun(t.Context(), lane.RunID)
			if err != nil || after.Version != before.Version || after.State != before.State || after.Tasks["task-1"].Attempts != 0 {
				t.Fatalf("refusal changed execution: %+v %v", after, err)
			}
			afterBytes, err := os.ReadFile(approvalPath)
			if err != nil || !bytes.Equal(approvalBytes, afterBytes) {
				t.Fatal("refusal changed original approval bytes")
			}
		})
	}
}

func TestExtremeTaskLaneApprovedInitialRequestReopensExactIdleLane(t *testing.T) {
	e, parent, base := laneFixture(t)
	lane, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-1", base)
	if err != nil {
		t.Fatal(err)
	}
	task := lane.Tasks["task-1"]
	task.ApprovalRequired = true
	lane.Tasks["task-1"] = task
	if err := e.store.UpdateRun(t.Context(), *lane); err != nil {
		t.Fatal(err)
	}
	waiting, err := e.ExecuteTaskBound(t.Context(), lane.RunID, "task-1", base)
	if err != nil || waiting.State != RunNeedsApproval {
		t.Fatalf("actual approval request: %+v %v", waiting, err)
	}
	id := waiting.Tasks["task-1"].ApprovalID
	if id == "" {
		t.Fatal("approval request absent")
	}
	if err := e.DecideApproval(t.Context(), id, true, "fixture-operator", "exact scope reviewed"); err != nil {
		t.Fatal(err)
	}
	approved, err := e.GetRun(t.Context(), lane.RunID)
	if err != nil || approved.State != RunPaused || approved.Tasks["task-1"].State != TaskReady {
		t.Fatalf("approval invented active provider: %+v %v", approved, err)
	}
	reopened, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-1", base)
	if err != nil || reopened.RunID != lane.RunID {
		t.Fatalf("exact approved lane failed reopen: %+v %v", reopened, err)
	}
	receipt, err := e.ApprovalManager().GetApproval(id)
	if err != nil || receipt.Status != ApprovalApproved {
		t.Fatalf("approval receipt lost: %+v %v", receipt, err)
	}
}
