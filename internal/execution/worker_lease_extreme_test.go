//go:build !governed

package execution

import (
	"context"
	"testing"
	"time"
)

func TestExtremeWorkerAttemptRejectsExpiredReleasedAndChangedParent(t *testing.T) {
	for _, fault := range []string{"released", "expired", "changed-parent"} {
		t.Run(fault, func(t *testing.T) {
			e, parent, base := laneFixture(t)
			started := make(chan TaskExecution, 1)
			release := make(chan struct{})
			e.RegisterHarness(NewMockHarness("mock", func(_ context.Context, task TaskExecution, _ ConstraintPackage, _ string) (TaskResult, error) {
				started <- task
				<-release
				return TaskResult{}, nil
			}))
			lane, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-2", base)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() { defer close(done); _, _ = e.ExecuteTaskBound(t.Context(), lane.RunID, "task-2", base) }()
			var task TaskExecution
			select {
			case task = <-started:
			case <-time.After(5 * time.Second):
				close(release)
				t.Fatal("provider did not start")
			}
			defer func() { close(release); <-done }()
			current, err := e.GetRun(t.Context(), lane.RunID)
			if err != nil || e.ValidateWorkerAttempt(t.Context(), current, task) != nil {
				t.Fatal("genuine active attempt refused")
			}
			switch fault {
			case "released":
				if err := e.leases.ReleaseLease(task.LeaseID, task.AssignedRole, nowUTC()); err != nil {
					t.Fatal(err)
				}
			case "expired":
				if err := e.leases.RenewLease(task.LeaseID, task.AssignedRole, time.Nanosecond, time.Now()); err != nil {
					t.Fatal(err)
				}
			case "changed-parent":
				latest, err := e.GetRun(t.Context(), parent.RunID)
				if err != nil {
					t.Fatal(err)
				}
				v := latest.Tasks[task.TaskID]
				v.AssignedHarness = "foreign"
				latest.Tasks[task.TaskID] = v
				if err := e.store.UpdateRun(t.Context(), latest); err != nil {
					t.Fatal(err)
				}
			}
			if e.ValidateWorkerAttempt(t.Context(), current, task) == nil {
				t.Fatal("stale worker attempt retained authority")
			}
		})
	}
}
