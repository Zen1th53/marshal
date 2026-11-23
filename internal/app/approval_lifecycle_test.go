package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/google/uuid"
)

func setupSerialApprovalFixture(t *testing.T, runName string) (context.Context, *Runtime, *MarshalService, string, *atomic.Int32) {
	t.Helper()
	ctx := t.Context()
	fixture, _ := marshalUserCloseFixture(t, 1)
	runtime := requiredProcess05TestRuntime(t, fixture)
	if runtime.layout.Database == "" {
		runtime.layout.Database = filepath.Join(runtime.layout.RuntimeDir, "state.db")
	}
	ctx = requiredTestOwnerContext(t, runtime, ctx)
	worker := requiredProcess05TestHarnessName("test-harness")
	projectRecord, err := runtime.Store().Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	goal := planGoal()
	goal.ID, goal.SessionID, goal.ProjectID = "GOAL-"+runName, "SESSION-"+runName, requiredProcess05TestProjectID(runtime, projectRecord.ID)
	if err := runtime.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	request := requiredProcess05PlanRequest(planCreateRequest())
	request.SessionID, request.ProjectID = goal.SessionID, projectid.ID(requiredProcess05TestProjectID(runtime, projectRecord.ID))
	p, err := runtime.Plans().Create(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	p.Checks = map[string][]string{"fix": {"grep -q fixed README.md"}}
	if err := runtime.Store().SavePlan(ctx, p, p.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Plans().Approve(ctx, projectid.ID(requiredProcess05TestProjectID(runtime, projectRecord.ID))); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	register := func(r *Runtime) {
		requiredRegisterProcess05TestHarness(t, r, worker, func(ctx context.Context, task execution.TaskExecution, _ execution.ConstraintPackage, worktree string) (execution.TaskResult, error) {
			calls.Add(1)
			trap, err := r.armHoneypot(worktree)
			if err != nil {
				return execution.TaskResult{}, err
			}
			t.Cleanup(func() { _ = trap.Close() })
			if err := os.WriteFile(filepath.Join(worktree, "README.md"), []byte("fixed\n"), 0600); err != nil {
				return execution.TaskResult{}, err
			}
			if _, err := gitMarshal(ctx, worktree, "add", "README.md"); err != nil {
				return execution.TaskResult{}, err
			}
			if _, err := gitMarshal(ctx, worktree, "commit", "-am", "fixed"); err != nil {
				return execution.TaskResult{}, err
			}
			return execution.TaskResult{TaskID: task.TaskID, Success: true}, nil
		})
	}
	register(runtime)
	wiring := MarshalWiring{Provider: "codex", Approver: func(context.Context, string, string) (string, error) { return "fixture-operator", nil }}
	s, err := runtime.MarshalWired(wiring)
	if err != nil {
		t.Fatal(err)
	}
	s.GovernedCheck = fixtureCheckRunner
	s.Verify = fixture.Verify
	s.VerifierProvider = fixture.VerifierProvider
	s.IndependentVerify = fixture.IndependentVerify
	s.Model = marshalFakeModel{review: marshal.Review{ArchitectureVerdict: "conforms", Reviewer: "marshal-model", Verdict: marshal.VerdictAccept}}
	s.CrossReview = func(context.Context, marshal.Task, marshal.HandIn, marshal.Control) (marshal.Review, string, error) {
		return marshal.Review{ArchitectureVerdict: "conforms", Verdict: marshal.VerdictAccept, Reviewer: "second", EvidenceRefs: []string{"check:grep -q fixed README.md"}}, "review-test", nil
	}
	s.GovernedDrivers[worker] = driver.Governed{Provider: worker, Run: runtime.marshalProcess05Run(s)}
	s.InstalledVersion = func(context.Context, string) string { return "1.0" }
	if err := fixture.Store.SaveHarnessProfile(ctx, model.HarnessProfile{Harness: worker, InstalledVersion: "1.0", ProbeEvidenceID: runName + "-probe", ProbedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BindApprovedPlan(ctx, runName); err != nil {
		t.Fatal(err)
	}
	return ctx, runtime, s, worker, &calls
}

func decideApprovalByEdition(t *testing.T, ctx context.Context, r *Runtime, sessionID, approvalID string, approve bool, reason string) {
	t.Helper()
	if isExtremeBuild {
		var err error
		if approve {
			err = r.Execution().Approve(ctx, approvalID, "fixture-operator", reason)
		} else {
			err = r.Execution().Reject(ctx, approvalID, "fixture-operator", reason)
		}
		if err != nil {
			t.Fatalf("default approve=%v failed: %v", approve, err)
		}
		return
	}
	local, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	record, err := r.ResolveDecision(ctx, sessionID, "execution:"+approvalID)
	if err != nil {
		t.Fatalf("resolve execution:%s: %v", approvalID, err)
	}
	_, err = r.CommandDecideApproval(local.Context(ctx), ApprovalDecision{
		Envelope: CommandEnvelope{
			ProjectID:       r.ProjectIdentity(),
			SessionID:       sessionID,
			TargetID:        record.ID,
			ExpectedVersion: record.Version,
			IdempotencyKey:  uuid.NewString(),
		},
		Digest:  record.Digest,
		Approve: approve,
		Reason:  reason,
	})
	if err != nil {
		t.Fatalf("governed CommandDecideApproval approve=%v failed: %v", approve, err)
	}
}

func TestExpiredSerialApprovalCanRequestReplacement(t *testing.T) {
	ctx, runtime, s, worker, calls := setupSerialApprovalFixture(t, "serial-replace")
	sessionID := "SESSION-serial-replace"
	brief := func(marshal.Task, BriefContext) string { return "fix the typo" }

	// 1. Initial execution stops on serial TASK_EXECUTE approval.
	waiting, err := s.Execute(ctx, "serial-replace", brief, nil)
	runs, listErr := runtime.Execution().ListRuns(ctx)
	if listErr != nil || len(runs) != 1 {
		t.Fatalf("expected 1 execution run: runs=%+v err=%v", runs, listErr)
	}
	assertMarshalProcess05Pause(t, s, "serial-replace", "fix", waiting, err, runs[0])
	firstID := runs[0].Tasks["fix"].ApprovalID
	firstReceipt, err := runtime.Execution().Engine().ApprovalManager().GetApproval(firstID)
	if err != nil || firstReceipt.ExpiresAt == nil {
		t.Fatalf("initial receipt: %+v %v", firstReceipt, err)
	}
	if calls.Load() != 0 {
		t.Fatalf("worker ran before approval: %d", calls.Load())
	}

	// 2. Advance clock past expiry. In default edition, Resume must still refuse
	// without mutating the expired receipt.
	expiredClock := firstReceipt.ExpiresAt.Add(time.Second)
	s.now = func() time.Time { return expiredClock }
	if isExtremeBuild {
		if _, resumeErr := s.Resume(ctx, "serial-replace"); resumeErr == nil || !strings.Contains(resumeErr.Error(), "expired") {
			t.Fatalf("expected serial expired refusal on resume, got: %v", resumeErr)
		}
	}

	// 3. Explicit owner retry clears old expired binding without rework charge.
	if err := s.RetryTask(ctx, "serial-replace", "fix", false); err != nil {
		t.Fatalf("explicit owner retry of expired serial approval failed: %v", err)
	}
	expiredReceipt, err := runtime.Execution().Engine().ApprovalManager().GetApproval(firstID)
	if err != nil || expiredReceipt.Status != execution.ApprovalExpired {
		t.Fatalf("old approval should be durably EXPIRED, got: %+v %v", expiredReceipt, err)
	}
	afterRetry, err := s.Snapshot(ctx, "serial-replace")
	if err != nil {
		t.Fatal(err)
	}
	if len(afterRetry.Tasks[0].ReturnsByAgent) != 0 || afterRetry.Tasks[0].Process05WaitApprovalID != "" {
		t.Fatalf("retry charged rework or kept stale wait binding: %+v", afterRetry.Tasks[0])
	}

	// 4. Reset clock to current time and execute: a fresh approval ticket is
	// created through normal authority without running the worker.
	s.now = func() time.Time { return time.Now().UTC() }
	waiting2, err2 := s.Execute(ctx, "serial-replace", brief, nil)
	runs2, err := runtime.Execution().ListRuns(ctx)
	if err != nil || len(runs2) != 1 {
		t.Fatalf("list runs after retry: %+v %v", runs2, err)
	}
	assertMarshalProcess05Pause(t, s, "serial-replace", "fix", waiting2, err2, runs2[0])
	secondID := runs2[0].Tasks["fix"].ApprovalID
	if secondID == "" || secondID == firstID {
		t.Fatalf("expected fresh replacement approval id != %s, got %q", firstID, secondID)
	}
	if calls.Load() != 0 {
		t.Fatalf("worker ran before replacement decision: %d", calls.Load())
	}

	// 5. Resuming before deciding the fresh ticket must still NOT execute the worker.
	if isExtremeBuild {
		stillWaiting, err := s.Resume(ctx, "serial-replace")
		if err != nil || stillWaiting.State != marshal.AwaitingUser || calls.Load() != 0 {
			t.Fatalf("resumed before valid decision: %+v calls=%d err=%v", stillWaiting, calls.Load(), err)
		}
	}

	// 6. Decide the fresh ticket through the edition's normal authority, then resume & execute.
	decideApprovalByEdition(t, ctx, runtime, sessionID, secondID, true, "approve replacement ticket")
	if _, err := s.Resume(ctx, "serial-replace"); err != nil {
		t.Fatalf("resume after valid replacement decision failed: %v", err)
	}
	final, err := s.Execute(ctx, "serial-replace", brief, nil)
	if err != nil {
		t.Fatalf("execute after valid replacement decision failed: %v", err)
	}
	if calls.Load() != 1 || final.Tasks[0].State != marshal.Merged {
		t.Fatalf("expected 1 worker run and Merged task, got calls=%d state=%s", calls.Load(), final.Tasks[0].State)
	}
	if final.Tasks[0].ReturnsByAgent[worker] != 0 {
		t.Fatalf("expired approval consumed worker rework: %v", final.Tasks[0].ReturnsByAgent)
	}
}

func TestApprovalExpiryDoesNotConsumeRework(t *testing.T) {
	ctx, runtime, s, worker, calls := setupSerialApprovalFixture(t, "expiry-no-rework")
	sessionID := "SESSION-expiry-no-rework"
	brief := func(marshal.Task, BriefContext) string { return "fix the typo" }

	// Expire and recover THREE times (more than default ReworkLimit=2) to prove
	// approval expiry never consumes worker rework or triggers escalation.
	seen := map[string]bool{}
	for cycle := 0; cycle < 3; cycle++ {
		s.now = func() time.Time { return time.Now().UTC() }
		_, _ = s.Execute(ctx, "expiry-no-rework", brief, nil)
		runs, err := runtime.Execution().ListRuns(ctx)
		if err != nil || len(runs) != 1 {
			t.Fatalf("cycle %d list runs: %v", cycle, err)
		}
		appID := runs[0].Tasks["fix"].ApprovalID
		if appID == "" || seen[appID] {
			t.Fatalf("cycle %d expected fresh approval id, got %q (seen=%v)", cycle, appID, seen)
		}
		seen[appID] = true
		rec, err := runtime.Execution().Engine().ApprovalManager().GetApproval(appID)
		if err != nil || rec.ExpiresAt == nil {
			t.Fatalf("cycle %d receipt: %+v %v", cycle, rec, err)
		}
		s.now = func() time.Time { return rec.ExpiresAt.Add(time.Second) }
		if err := s.RetryTask(ctx, "expiry-no-rework", "fix", false); err != nil {
			t.Fatalf("cycle %d retry expired wait: %v", cycle, err)
		}
		snap, err := s.Snapshot(ctx, "expiry-no-rework")
		if err != nil {
			t.Fatal(err)
		}
		if snap.Tasks[0].State == marshal.Escalated || snap.Tasks[0].ReturnsByAgent[worker] != 0 || snap.Tasks[0].FreshSessionRetries != 0 {
			t.Fatalf("cycle %d expiry consumed rework or escalated: %+v", cycle, snap.Tasks[0])
		}
	}

	// Fourth fresh ticket is approved and finishes cleanly with 0 rework charged.
	s.now = func() time.Time { return time.Now().UTC() }
	_, _ = s.Execute(ctx, "expiry-no-rework", brief, nil)
	runs, err := runtime.Execution().ListRuns(ctx)
	if err != nil || len(runs) != 1 {
		t.Fatalf("final list runs: %v", err)
	}
	finalID := runs[0].Tasks["fix"].ApprovalID
	decideApprovalByEdition(t, ctx, runtime, sessionID, finalID, true, "approve after expiries")
	if _, err := s.Resume(ctx, "expiry-no-rework"); err != nil {
		t.Fatal(err)
	}
	done, err := s.Execute(ctx, "expiry-no-rework", brief, nil)
	if err != nil {
		t.Fatalf("final execute: %v", err)
	}
	if calls.Load() != 1 || done.Tasks[0].State != marshal.Merged || done.Tasks[0].ReturnsByAgent[worker] != 0 {
		t.Fatalf("unexpected final state: calls=%d task=%+v", calls.Load(), done.Tasks[0])
	}
}

func TestApprovalTerminalTransitionMatrix(t *testing.T) {
	ctx := t.Context()
	r := runtimeForPlan(t)
	canonicalDecisionPlan(t, r)
	project := projectid.ID(r.ProjectIdentity())
	if _, err := r.Plans().Approve(ctx, project); err != nil {
		t.Fatal(err)
	}
	execRun, err := r.Execution().StartRun(ctx, "SESSION-plan", project)
	if err != nil {
		t.Fatal(err)
	}
	mgr := r.Execution().Engine().ApprovalManager()
	base := time.Now().UTC().Truncate(time.Second)

	newTicket := func(task string) *execution.RuntimeApproval {
		t.Helper()
		a, err := mgr.RequestApproval(execution.ApprovalRequest{
			RequestedBy:    "worker-matrix",
			RunID:          execRun.RunID,
			TaskID:         task,
			PlanID:         execRun.PlanID,
			PlanVersion:    execRun.PlanVersion,
			Attempt:        1,
			OperationType:  "TASK_EXECUTE",
			TargetResource: "README.md",
			RiskLevel:      model.R2,
			Scope:          "task_mutation",
			Parameters:     "matrix " + task,
			TTL:            15 * time.Minute,
			Now:            base,
		})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}

	// 1. Eligible allow -> APPROVED -> consumable once with exact binding -> CONSUMED.
	t.Run("allow-and-consume-once", func(t *testing.T) {
		a := newTicket("task-1")
		decided, err := mgr.Decide(a.ApprovalID, true, "owner-1", "ok", base.Add(time.Minute))
		if err != nil || decided.Status != execution.ApprovalApproved {
			t.Fatalf("allow failed: %+v %v", decided, err)
		}
		if err := mgr.ValidateAndConsume(a.ApprovalID, a.ActionDigest, a.StateDigest, base.Add(2*time.Minute)); err != nil {
			t.Fatalf("first consume failed: %v", err)
		}
		if err := mgr.ValidateAndConsume(a.ApprovalID, a.ActionDigest, a.StateDigest, base.Add(3*time.Minute)); err == nil {
			t.Fatal("second consume unexpectedly succeeded")
		}
	})

	// 2. Deny -> DENIED and visible blocked task on execution run.
	t.Run("deny-blocks-task", func(t *testing.T) {
		var planTaskID string
		for id := range execRun.Tasks {
			planTaskID = id
			break
		}
		a := newTicket(planTaskID)
		if err := r.Execution().Reject(ctx, a.ApprovalID, "owner-1", "too risky"); err != nil {
			t.Fatalf("reject failed: %v", err)
		}
		got, err := mgr.GetApproval(a.ApprovalID)
		if err != nil || got.Status != execution.ApprovalDenied {
			t.Fatalf("expected DENIED, got %+v %v", got, err)
		}
		updatedRun, err := r.Execution().GetRun(ctx, execRun.RunID)
		if err != nil || updatedRun.Tasks[planTaskID].State != execution.TaskBlocked {
			t.Fatalf("expected blocked task %s, got %+v %v", planTaskID, updatedRun.Tasks[planTaskID], err)
		}
	})

	// 3. Deadline -> EXPIRED.
	t.Run("deadline-expires", func(t *testing.T) {
		a := newTicket("task-1")
		after, changed, err := r.ExpireApproval(ctx, "RUN-matrix", a.ApprovalID, a.ExpiresAt.Add(time.Second))
		if err != nil || !changed || after.Status != execution.ApprovalExpired {
			t.Fatalf("expire failed: changed=%v after=%+v err=%v", changed, after, err)
		}
		// Late decision returns existing EXPIRED state.
		late, lateErr := mgr.Decide(a.ApprovalID, true, "owner-1", "late", a.ExpiresAt.Add(2*time.Second))
		if lateErr == nil || late == nil || late.Status != execution.ApprovalExpired {
			t.Fatalf("late decision on expired: %+v %v", late, lateErr)
		}
	})

	// 4. Stronger replacement -> SUPERSEDED with replacement pointer, late decision returns pointer.
	t.Run("supersede-with-replacement", func(t *testing.T) {
		old := newTicket("task-1")
		replacement := newTicket("task-1")
		after, changed, err := r.SupersedeApproval(ctx, "RUN-matrix", old.ApprovalID, replacement.ApprovalID, "stronger scope", base.Add(time.Minute))
		if err != nil || !changed || after.Status != execution.ApprovalSuperseded || after.ReplacementID != replacement.ApprovalID {
			t.Fatalf("supersede failed: %+v changed=%v err=%v", after, changed, err)
		}
		late, lateErr := mgr.Decide(old.ApprovalID, true, "owner-1", "late", base.Add(2*time.Minute))
		if lateErr == nil || late == nil || late.Status != execution.ApprovalSuperseded || late.ReplacementID != replacement.ApprovalID {
			t.Fatalf("late decision did not return replacement pointer: %+v %v", late, lateErr)
		}
	})

	// 5. Run close -> CLOSED/revoked.
	t.Run("run-close-revokes", func(t *testing.T) {
		a := newTicket("task-1")
		if err := r.RecordApprovalOpened(ctx, "RUN-matrix", *a); err != nil {
			t.Fatal(err)
		}
		mrun := marshal.Run{
			PlanID:      execRun.PlanID,
			PlanVersion: execRun.PlanVersion,
			Tasks:       []marshal.Task{{PlanTaskID: "task-1"}},
		}
		closed, err := r.CloseRunApprovals(ctx, "RUN-matrix", mrun, "run closed", base.Add(time.Minute))
		if err != nil || len(closed) == 0 {
			t.Fatalf("CloseRunApprovals: closed=%+v err=%v", closed, err)
		}
		got, err := mgr.GetApproval(a.ApprovalID)
		if err != nil || got.Status != execution.ApprovalClosed || !got.Revoked {
			t.Fatalf("expected CLOSED+revoked, got %+v %v", got, err)
		}
	})

	// 6. Interrupted with intact deadline & binding -> remain REQUESTED and replay;
	// otherwise close as INTERRUPTED/revoked.
	t.Run("interrupted-reconcile", func(t *testing.T) {
		intact := newTicket("task-1")
		after, changed, err := r.ReconcileInterruptedApproval(ctx, "RUN-matrix", intact.ApprovalID, true, "restart", base.Add(time.Minute))
		if err != nil || changed || after.Status != execution.ApprovalRequested {
			t.Fatalf("intact interrupted should remain REQUESTED: changed=%v after=%+v err=%v", changed, after, err)
		}
		broken := newTicket("task-1")
		afterBroken, changedBroken, err := r.ReconcileInterruptedApproval(ctx, "RUN-matrix", broken.ApprovalID, false, "binding lost", base.Add(time.Minute))
		if err != nil || !changedBroken || afterBroken.Status != execution.ApprovalInterrupted || !afterBroken.Revoked {
			t.Fatalf("broken interrupted should close as INTERRUPTED/revoked: changed=%v after=%+v err=%v", changedBroken, afterBroken, err)
		}
	})

	// 7. Post-allow before consume: close or supersession invalidates grant under
	// the same fence, records revocation, and blocks consumption.
	t.Run("post-allow-revocation-before-consume", func(t *testing.T) {
		// 7a. Close after allow before consume
		aClose := newTicket("task-1")
		if _, err := mgr.Decide(aClose.ApprovalID, true, "owner-1", "allow", base.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		closedApp, changed, err := mgr.CloseApproval(aClose.ApprovalID, "run closed before consume", base.Add(2*time.Minute))
		if err != nil || !changed || closedApp.Status != execution.ApprovalClosed || !closedApp.Revoked {
			t.Fatalf("close after allow failed: %+v changed=%v err=%v", closedApp, changed, err)
		}
		if err := r.RecordApprovalTerminal(ctx, "RUN-matrix", *closedApp, "revoked", "run closed before consume"); err != nil {
			t.Fatal(err)
		}
		if err := mgr.ValidateAndConsume(aClose.ApprovalID, aClose.ActionDigest, aClose.StateDigest, base.Add(3*time.Minute)); err == nil {
			t.Fatal("consumed grant revoked by close")
		}

		// 7b. Supersession after allow before consume
		aSup := newTicket("task-1")
		rep := newTicket("task-1")
		if _, err := mgr.Decide(aSup.ApprovalID, true, "owner-1", "allow", base.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		supApp, changed, err := r.SupersedeApproval(ctx, "RUN-matrix", aSup.ApprovalID, rep.ApprovalID, "superseded before consume", base.Add(2*time.Minute))
		if err != nil || !changed || supApp.Status != execution.ApprovalSuperseded || !supApp.Revoked || supApp.ReplacementID != rep.ApprovalID {
			t.Fatalf("supersede after allow failed: %+v changed=%v err=%v", supApp, changed, err)
		}
		if err := mgr.ValidateAndConsume(aSup.ApprovalID, aSup.ActionDigest, aSup.StateDigest, base.Add(3*time.Minute)); err == nil {
			t.Fatal("consumed grant revoked by supersession")
		}
	})
}

func TestApprovalOutboxAckReplay(t *testing.T) {
	ctx := t.Context()
	r := runtimeForPlan(t)
	mgr := r.Execution().Engine().ApprovalManager()
	now := time.Now().UTC().Truncate(time.Second)

	a, err := mgr.RequestApproval(execution.ApprovalRequest{
		RequestedBy:    "worker-outbox",
		RunID:          "exec-outbox",
		TaskID:         "task-outbox",
		PlanID:         "plan-outbox",
		PlanVersion:    1,
		Attempt:        1,
		OperationType:  "TASK_EXECUTE",
		TargetResource: "app.go Bearer sk-secret-token-value-1234567890",
		RiskLevel:      model.R2,
		Scope:          "task_mutation",
		TTL:            10 * time.Minute,
		Now:            now,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Write terminal BEFORE opened to prove ordering STILL queues opened ahead of closed.
	expired, changed, err := r.ExpireApproval(ctx, "RUN-outbox", a.ApprovalID, a.ExpiresAt.Add(time.Second))
	if err != nil || !changed || expired.Status != execution.ApprovalExpired {
		t.Fatalf("expire failed: %+v %v", expired, err)
	}
	if err := r.RecordApprovalOpened(ctx, "RUN-outbox", *a); err != nil {
		t.Fatalf("record opened failed: %v", err)
	}

	// Verify both outbox entries and audit events exist durably.
	pending, err := r.Store().PendingNotices(ctx, r.ProjectIdentity(), "RUN-outbox", 10)
	if err != nil || len(pending) != 2 {
		t.Fatalf("expected 2 pending notices, got %+v err=%v", pending, err)
	}
	if pending[0].Transition != "opened" || pending[1].Transition != "expired" {
		t.Fatalf("expected opened queued ahead of terminal, got order: %s then %s", pending[0].Transition, pending[1].Transition)
	}
	if strings.Contains(pending[0].Summary, "sk-secret-token") || strings.Contains(pending[0].Command, "sk-secret-token") {
		t.Fatalf("outbox leaked secret: %+v", pending[0])
	}

	// Verify busy/absent chat error leaves entries unacked without blocking lifecycle.
	if _, err := r.DeliverPendingNotices(ctx, "marshal-chat", "RUN-outbox", func(store.NoticeOutboxEntry, string) error {
		return errors.New("chat busy")
	}); err == nil {
		t.Fatal("expected busy delivery error")
	}
	stillPending, err := r.Store().PendingNotices(ctx, r.ProjectIdentity(), "RUN-outbox", 10)
	if err != nil || len(stillPending) != 2 {
		t.Fatalf("busy chat unexpectedly acked notices: %+v %v", stillPending, err)
	}

	// Simulate crash AFTER admitting first event into durable accepting inbox, BEFORE ack!
	if _, err := r.Store().AdmitNotice(ctx, "marshal-chat", pending[0].EventID, "pre-crash delivered", now); err != nil {
		t.Fatal(err)
	}

	// Replay: first event is already in accepting inbox (deduped, NOT delivered a second time!),
	// second event is delivered once, and BOTH are acked!
	var delivered []string
	var deliveredEntries []store.NoticeOutboxEntry
	acked, err := r.DeliverPendingNotices(ctx, "marshal-chat", "RUN-outbox", func(entry store.NoticeOutboxEntry, rendered string) error {
		deliveredEntries = append(deliveredEntries, entry)
		delivered = append(delivered, rendered)
		return nil
	})
	if err != nil {
		t.Fatalf("replay delivery failed: %v", err)
	}
	if acked != 2 {
		t.Fatalf("expected 2 acked on replay, got %d", acked)
	}
	if len(delivered) != 1 || deliveredEntries[0].EventID != pending[1].EventID {
		t.Fatalf("expected dedup of already-admitted event 0 and delivery of event 1 only; got entries=%+v", deliveredEntries)
	}

	// Second consumer (fresh accepting inbox) receives BOTH in order, and opened
	// notice delivered AFTER expiry includes current status [current status: EXPIRED].
	a2, err := mgr.RequestApproval(execution.ApprovalRequest{
		RequestedBy:    "worker-outbox",
		RunID:          "exec-outbox",
		TaskID:         "task-outbox-2",
		PlanID:         "plan-outbox",
		PlanVersion:    1,
		OperationType:  "TASK_EXECUTE",
		TargetResource: "b.go",
		RiskLevel:      model.R2,
		Scope:          "task_mutation",
		TTL:            10 * time.Minute,
		Now:            now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.RecordApprovalOpened(ctx, "RUN-outbox-late", *a2); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.ExpireApproval(ctx, "RUN-outbox-late", a2.ApprovalID, a2.ExpiresAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var lateDelivered []string
	acked2, err := r.DeliverPendingNotices(ctx, "marshal-chat", "RUN-outbox-late", func(_ store.NoticeOutboxEntry, rendered string) error {
		lateDelivered = append(lateDelivered, rendered)
		return nil
	})
	if err != nil || acked2 != 2 || len(lateDelivered) != 2 {
		t.Fatalf("late delivery: acked=%d delivered=%v err=%v", acked2, lateDelivered, err)
	}
	if !strings.Contains(lateDelivered[0], "current status: EXPIRED") {
		t.Fatalf("opened notice delivered after expiry did not show current status: %q", lateDelivered[0])
	}
}

func TestGovernedOperatorApprovalIsNotPrivateWaitStub(t *testing.T) {
	ctx := t.Context()
	r := runtimeForPlan(t)
	canonicalDecisionPlan(t, r)
	project := projectid.ID(r.ProjectIdentity())
	if _, err := r.Plans().Approve(ctx, project); err != nil {
		t.Fatal(err)
	}
	execRun, err := r.Execution().StartRun(ctx, "SESSION-plan", project)
	if err != nil {
		t.Fatal(err)
	}
	a, err := r.Execution().Engine().ApprovalManager().RequestApproval(execution.ApprovalRequest{
		RequestedBy:    "worker-gov",
		RunID:          execRun.RunID,
		TaskID:         "task-1",
		PlanID:         execRun.PlanID,
		PlanVersion:    execRun.PlanVersion,
		Attempt:        1,
		OperationType:  "TASK_EXECUTE",
		TargetResource: "README.md",
		RiskLevel:      model.R2,
		Scope:          "task_mutation",
		Parameters:     "governed operator path",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify edition-appropriate private wait binding contract:
	// In governed edition, private wait binding is a stub (false), whereas in
	// default edition it carries the exact binding.
	waitErr := process05ApprovalRequired(execRun.RunID, "task-1", a.ApprovalID)
	_, _, boundID, waiting := MarshalApprovalWaitBinding(waitErr)
	if isExtremeBuild {
		if !waiting || boundID != a.ApprovalID {
			t.Fatalf("default edition wait binding missing: waiting=%v id=%q", waiting, boundID)
		}
	} else {
		if waiting {
			t.Fatalf("governed edition must keep private wait binding as stub, got waiting=true id=%q", boundID)
		}
		if !strings.Contains(waitErr.Error(), "/approve execution:"+a.ApprovalID) {
			t.Fatalf("governed approval error should name /approve execution:%s, got %q", a.ApprovalID, waitErr.Error())
		}
	}

	// In BOTH editions, the authenticated operator decision path
	// Runtime.CommandDecideApproval requires local operator auth and exact typed ID.
	record, err := r.ResolveDecision(ctx, "SESSION-plan", "execution:"+a.ApprovalID)
	if err != nil {
		t.Fatalf("ResolveDecision failed: %v", err)
	}
	req := ApprovalDecision{
		Envelope: CommandEnvelope{
			ProjectID:       r.ProjectIdentity(),
			SessionID:       "SESSION-plan",
			TargetID:        record.ID,
			ExpectedVersion: record.Version,
			IdempotencyKey:  uuid.NewString(),
		},
		Digest:  record.Digest,
		Approve: true,
		Reason:  "authenticated operator decision",
	}

	// Unauthenticated context MUST be refused.
	if _, err := r.CommandDecideApproval(ctx, req); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("unauthenticated CommandDecideApproval must fail with ErrDenied, got: %v", err)
	}

	// Authenticated local operator succeeds and records the real principal actor.
	local, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ownerCtx := local.Context(ctx)
	actor, ok := auth.LocalFromContext(ownerCtx)
	if !ok || actor.ID() == "" {
		t.Fatal("missing local operator principal")
	}
	decided, err := r.CommandDecideApproval(ownerCtx, req)
	if err != nil {
		t.Fatalf("authenticated CommandDecideApproval failed: %v", err)
	}
	if decided.Status != string(execution.ApprovalApproved) {
		t.Fatalf("expected APPROVED, got %+v", decided)
	}
	stored, err := r.Execution().Engine().ApprovalManager().GetApproval(a.ApprovalID)
	if err != nil || stored.ApprovedBy != actor.ID() {
		t.Fatalf("expected proper decision actor %q, got %+v %v", actor.ID(), stored, err)
	}
	// Terminal outbox notice was recorded for the operator decision.
	notices, err := r.Store().ListNotices(ctx, r.ProjectIdentity(), "", 20)
	if err != nil {
		t.Fatal(err)
	}
	foundTerminal := false
	for _, n := range notices {
		if n.ApprovalID == a.ApprovalID && n.Transition == "approved" {
			foundTerminal = true
			break
		}
	}
	if !foundTerminal {
		t.Fatalf("expected terminal approved notice in outbox for %s, got: %+v", a.ApprovalID, notices)
	}
}
