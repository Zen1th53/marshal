package app

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestApprovalNoticeDeliveryResumesAfterAdmission(t *testing.T) {
	for _, alreadyDelivered := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-inbox-write", true: "after-inbox-write"}[alreadyDelivered], func(t *testing.T) {
			r := runtimeForPlan(t)
			ctx := t.Context()
			a, err := r.Execution().Engine().ApprovalManager().RequestApproval(execution.ApprovalRequest{RunID: "execution-replay", TaskID: "task", OperationType: "TASK_EXECUTE", TTL: time.Hour})
			if err != nil {
				t.Fatal(err)
			}
			if err := r.RecordApprovalOpened(ctx, "run-replay", *a); err != nil {
				t.Fatal(err)
			}
			pending, err := r.Store().PendingNotices(ctx, r.ProjectIdentity(), "run-replay", 10)
			if err != nil || len(pending) != 1 {
				t.Fatalf("pending notices: %+v %v", pending, err)
			}
			id := pending[0].EventID
			if admitted, err := r.Store().AdmitNotice(ctx, "chat", id, "", time.Now().UTC()); err != nil || !admitted {
				t.Fatalf("admit: %v %v", admitted, err)
			}
			// Model a durable destination that deduplicates by event ID.
			inbox := map[string]string{}
			if alreadyDelivered {
				inbox[id] = "previous delivery"
			}
			unavailable := errors.New("inbox unavailable")
			if acked, err := r.DeliverPendingNotices(ctx, "chat", "run-replay", func(store.NoticeOutboxEntry, string) error {
				return unavailable
			}); !errors.Is(err, unavailable) || acked != 0 {
				t.Fatalf("failed replay: %d %v", acked, err)
			}
			calls := 0
			deliver := func(entry store.NoticeOutboxEntry, rendered string) error {
				calls++
				if _, exists := inbox[entry.EventID]; !exists {
					inbox[entry.EventID] = rendered
				}
				return nil
			}
			if acked, err := r.DeliverPendingNotices(ctx, "chat", "run-replay", deliver); err != nil || acked != 1 {
				t.Fatalf("replay: %d %v", acked, err)
			}
			if acked, err := r.DeliverPendingNotices(ctx, "chat", "run-replay", deliver); err != nil || acked != 0 {
				t.Fatalf("acknowledged replay: %d %v", acked, err)
			}
			if calls != 1 || len(inbox) != 1 || inbox[id] == "" {
				t.Fatalf("destination: calls=%d inbox=%v", calls, inbox)
			}
		})
	}
}

func TestApprovalTerminalNoticeRecovery(t *testing.T) {
	for _, sweep := range []bool{false, true} {
		t.Run(map[bool]string{false: "expiry-retry", true: "delivery-sweep"}[sweep], func(t *testing.T) {
			r := runtimeForPlan(t)
			ctx := t.Context()
			mgr := r.Execution().Engine().ApprovalManager()
			a, err := mgr.RequestApproval(execution.ApprovalRequest{RunID: "execution-recovery", TaskID: "task", OperationType: "TASK_EXECUTE", TTL: time.Minute})
			if err != nil {
				t.Fatal(err)
			}
			if err := r.RecordApprovalOpened(ctx, "run-recovery", *a); err != nil {
				t.Fatal(err)
			}
			if _, changed, err := mgr.Expire(a.ApprovalID, a.ExpiresAt.Add(time.Second)); err != nil || !changed {
				t.Fatalf("expire: %v %v", changed, err)
			}
			if sweep {
				if _, err := r.DeliverPendingNotices(ctx, "chat", "run-recovery", nil); err != nil {
					t.Fatal(err)
				}
			} else {
				for range 2 {
					if _, changed, err := r.ExpireApproval(ctx, "run-recovery", a.ApprovalID, a.ExpiresAt.Add(time.Second)); err != nil || changed {
						t.Fatalf("retry: %v %v", changed, err)
					}
				}
			}
			notices, err := r.Store().ListNotices(ctx, r.ProjectIdentity(), "run-recovery", 10)
			if err != nil || len(notices) != 2 || notices[0].Status != string(execution.ApprovalExpired) {
				t.Fatalf("notices: %+v %v", notices, err)
			}
		})
	}
}

func TestApprovalNoticeConcurrentDelivery(t *testing.T) {
	r := runtimeForPlan(t)
	ctx := t.Context()
	a, err := r.Execution().Engine().ApprovalManager().RequestApproval(execution.ApprovalRequest{RunID: "execution-concurrent", TaskID: "task", OperationType: "TASK_EXECUTE", TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.RecordApprovalOpened(ctx, "run-concurrent", *a); err != nil {
		t.Fatal(err)
	}
	var delivered atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := r.DeliverPendingNotices(ctx, "chat", "run-concurrent", func(store.NoticeOutboxEntry, string) error { delivered.Add(1); return nil })
			if err != nil {
				t.Error(err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if got := delivered.Load(); got != 1 {
		t.Fatalf("deliveries: %d", got)
	}
}

func TestSerialExpiredApprovalRetrySaveConflict(t *testing.T) {
	ctx, r, s, _, _ := setupSerialApprovalFixture(t, "retry-save-conflict")
	if _, err := s.Execute(ctx, "retry-save-conflict", marshalBrief, nil); err != nil && (isExtremeBuild || !strings.Contains(err.Error(), "approval")) {
		t.Fatal(err)
	}
	runs, err := r.Execution().ListRuns(ctx)
	if err != nil || len(runs) != 1 {
		t.Fatalf("execution runs: %+v %v", runs, err)
	}
	id := runs[0].Tasks["fix"].ApprovalID
	a, err := r.Execution().Engine().ApprovalManager().GetApproval(id)
	if err != nil || a.ExpiresAt == nil {
		t.Fatalf("approval: %+v %v", a, err)
	}
	now := a.ExpiresAt.Add(time.Second)
	s.now = func() time.Time { return now }
	run, rev, err := s.load(ctx, "retry-save-conflict")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.save(ctx, "retry-save-conflict", run, rev); err != nil {
		t.Fatal(err)
	}
	handled, err := s.retryExpiredApprovalWait(ctx, "retry-save-conflict", run, rev, 0)
	if !handled || err == nil {
		t.Fatalf("stale retry: %v %v", handled, err)
	}
	actual, err := r.Execution().GetRun(ctx, runs[0].RunID)
	if err != nil || actual.Tasks["fix"].ApprovalID != id {
		t.Fatalf("binding after failed save: %+v %v", actual, err)
	}
	if err := s.RetryTask(ctx, "retry-save-conflict", "fix", false); err != nil {
		t.Fatal(err)
	}
	actual, err = r.Execution().GetRun(ctx, runs[0].RunID)
	if err != nil || actual.Tasks["fix"].ApprovalID != "" || actual.Tasks["fix"].State != execution.TaskPending {
		t.Fatalf("binding after retry: %+v %v", actual, err)
	}
}

func TestApprovalProjectionExactRun(t *testing.T) {
	r := runtimeForPlan(t)
	ctx := t.Context()
	run := marshal.Run{PlanID: "same-plan", PlanVersion: 1, Tasks: []marshal.Task{{PlanTaskID: "task"}}}
	var expected string
	for _, binding := range []string{"first-run", "second-run"} {
		a, err := r.Execution().Engine().ApprovalManager().RequestApproval(execution.ApprovalRequest{RunID: "shared-execution", TaskID: "task", PlanID: run.PlanID, PlanVersion: run.PlanVersion, OperationType: "TASK_EXECUTE", Parameters: binding, TTL: time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		if err := r.RecordApprovalOpened(ctx, binding, *a); err != nil {
			t.Fatal(err)
		}
		if binding == "first-run" {
			expected = a.ApprovalID
		}
		if err := r.Store().AckNotice(ctx, "approval:"+a.ApprovalID+":opened", time.Now().UTC().Add(-40*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Store().PruneNotices(ctx, time.Now().UTC().Add(-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	views, _, err := r.ApprovalProjection(ctx, "first-run", run, true)
	if err != nil || len(views) != 1 || views[0].ApprovalID != expected {
		t.Fatalf("exact run approvals: %+v %v", views, err)
	}
}
