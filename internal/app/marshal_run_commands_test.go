package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/store"
)

func runCommandForTest(t *testing.T, s *MarshalService, id, operation, task string) marshal.RunCommand {
	t.Helper()
	grant := capability.Grant{ID: "run-command-owner", Subject: "operator:test", TaskID: capability.TaskID(s.ProjectID), Kind: capability.KindFilesystemWrite,
		Scope: capability.Scope{Resource: "run-state", Actions: []string{"run.control"}}, IssuedAt: time.Now().Add(-time.Hour), ExpiresAt: time.Now().Add(time.Hour), Issuer: "operator:test"}
	if _, err := s.Store.GetCapabilityGrant(t.Context(), grant.ID); errors.Is(err, model.ErrNotFound) {
		if err = s.Store.PutCapabilityGrant(t.Context(), grant); err != nil {
			t.Fatal(err)
		}
	} else if err != nil {
		t.Fatal(err)
	}
	_, rev, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	return marshal.RunCommand{ID: id, Actor: string(grant.Subject), GrantID: string(grant.ID), AuthorityProject: s.ProjectID, Resource: grant.Scope.Resource,
		Digest: id + ":" + operation, Operation: operation, TaskID: task, Revision: rev, Reason: "revise the result"}
}

func queueRunCommandForTest(t *testing.T, s *MarshalService, c marshal.RunCommand) marshal.RunCommand {
	t.Helper()
	w := s.runWriter("run")
	if _, ok := w.startExecution("test-exec"); !ok {
		t.Fatal("writer already active")
	}
	receipt, err := s.enqueueRunCommand(t.Context(), "run", c)
	w.endExecution()
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func drainCommandsForTest(t *testing.T, s *MarshalService) marshal.Run {
	t.Helper()
	w := s.runWriter("run")
	w.mu.Lock()
	err := s.drainRunCommands(t.Context(), "run")
	w.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.Snapshot(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestQueuedCommandRejectsStaleAttempt(t *testing.T) {
	s := handedInFixture(t)
	c := runCommandForTest(t, s, "return-old", "return", "a")
	queueRunCommandForTest(t, s, c)
	run, rev, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	run.Tasks[0].EvidenceAttemptBase++
	if err = s.save(t.Context(), "run", run, rev); err != nil {
		t.Fatal(err)
	}
	run = drainCommandsForTest(t, s)
	if run.Commands[0].State != "refused" || !strings.Contains(run.Commands[0].Detail, "attempt") || run.Tasks[0].State != marshal.HandedIn {
		t.Fatalf("stale request applied: %+v", run.Commands[0])
	}
}

func TestDurableCommandsRecoverBeforeAndAfterEffect(t *testing.T) {
	for _, point := range []string{"queued", "revoked", "applying", "applied"} {
		t.Run(point, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "state.db")
			s, _ := marshalFixture(t, 1, dbPath)
			if _, err := s.StartPlanning(t.Context(), "run", "write file", marshal.Budget{}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Approve(t.Context(), "run"); err != nil {
				t.Fatal(err)
			}
			dispatch, err := s.Dispatch(t.Context(), "run", "a", "write")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.CollectHandIn(t.Context(), "run", dispatch); err != nil {
				t.Fatal(err)
			}
			c := runCommandForTest(t, s, "durable-return", "return", "a")
			queueRunCommandForTest(t, s, c)
			if point == "revoked" {
				if err := s.Store.RevokeCapabilityGrant(t.Context(), capability.GrantID(c.GrantID), time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
			}
			if point == "applied" {
				drainCommandsForTest(t, s)
			}
			if point == "applying" {
				run, rev, _ := s.load(t.Context(), "run")
				run.Commands[0].State = "applying"
				if err := s.save(t.Context(), "run", run, rev); err != nil {
					t.Fatal(err)
				}
			}
			// Reopen the same durable state through a separate connection.
			db, err := store.Open(t.Context(), dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			restarted := *s
			restarted.Store = db
			if err = restarted.RecoverPendingOperations(t.Context()); err != nil {
				t.Fatal(err)
			}
			run := drainCommandsForTest(t, &restarted)
			want := "applied"
			switch point {
			case "applying":
				want = "held"
			case "revoked":
				want = "refused"
			}
			if run.Commands[0].State != want {
				t.Fatalf("receipt=%+v", run.Commands[0])
			}
			before := taskReturns(run.Tasks[0])
			run = drainCommandsForTest(t, &restarted)
			if taskReturns(run.Tasks[0]) != before {
				t.Fatal("recovery replayed a return")
			}
			if point == "queued" || point == "applied" {
				if before != 1 {
					t.Fatalf("return count=%d", before)
				}
			} else if before != 0 {
				t.Fatalf("unexpected return count=%d for %s", before, point)
			}
		})
	}
}

func TestTaskActionEligibilityMatrix(t *testing.T) {
	for _, operation := range []string{"return", "accept", "reassign", "cancel"} {
		for _, state := range []marshal.TaskState{marshal.Queued, marshal.Dispatched, marshal.HandedIn, marshal.Escalated, marshal.Merged} {
			t.Run(operation+"/"+string(state), func(t *testing.T) {
				s := handedInFixture(t)
				run, rev, _ := s.load(t.Context(), "run")
				run.Tasks[0].State = state
				if err := s.save(t.Context(), "run", run, rev); err != nil {
					t.Fatal(err)
				}
				c := runCommandForTest(t, s, "action", operation, "a")
				c.Worker = "other"
				// Same-mode replacement is an existing supported driver.
				s.Drivers["other"] = s.Drivers["worker"]
				receipt, err := s.enqueueRunCommand(t.Context(), "run", c)
				eligible := (operation == "return" || operation == "accept") && state == marshal.HandedIn || (operation == "reassign" || operation == "cancel") && state == marshal.Escalated || operation == "reassign" && state == marshal.Queued && governedProviderSwitchAllowed
				applied := err == nil && receipt.State == "applied"
				if applied != eligible {
					t.Fatalf("eligible=%v receipt=%+v err=%v", eligible, receipt, err)
				}
			})
		}
	}
	for _, operation := range []string{"reassign", "cancel"} {
		t.Run(operation+"/permanent-escalation", func(t *testing.T) {
			s := handedInFixture(t)
			run, rev, _ := s.load(t.Context(), "run")
			run.Tasks[0].State = marshal.Escalated
			run.Tasks[0].EscalationReason = "security: honeypot triggered"
			run.Tasks[0].EscalationCode = marshal.CodeSecurity
			if err := s.save(t.Context(), "run", run, rev); err != nil {
				t.Fatal(err)
			}
			c := runCommandForTest(t, s, "perm-"+operation, operation, "a")
			c.Worker = "other"
			s.Drivers["other"] = s.Drivers["worker"]
			receipt, err := s.enqueueRunCommand(t.Context(), "run", c)
			if err != nil || receipt.State != "refused" {
				t.Fatalf("permanent escalation allowed %s: receipt=%+v err=%v", operation, receipt, err)
			}
		})
	}
	t.Run("accept/review-gate-refuses", func(t *testing.T) {
		s := handedInFixture(t)
		s.CrossReview = func(context.Context, marshal.Task, marshal.HandIn, marshal.Control) (marshal.Review, string, error) {
			return marshal.Review{
				Reviewer:     "independent-reviewer",
				Verdict:      marshal.VerdictReturn,
				Reasons:      []string{"missing edge test"},
				EvidenceRefs: []string{"check:0"},
				FreshSession: true,
			}, "other-provider", nil
		}
		c := runCommandForTest(t, s, "accept-rejected", "accept", "a")
		receipt, err := s.enqueueRunCommand(t.Context(), "run", c)
		if err != nil || receipt.State != "refused" {
			t.Fatalf("rejected review marked accept=%+v err=%v", receipt, err)
		}
	})
}

func TestPriorityPauseFencesNewStarts(t *testing.T) {
	s := handedInFixture(t)
	normal := runCommandForTest(t, s, "normal", "return", "a")
	queueRunCommandForTest(t, s, normal)
	pause := runCommandForTest(t, s, "pause", "pause", "")
	queueRunCommandForTest(t, s, pause)
	extra := runCommandForTest(t, s, "extra", "return", "a")
	if _, err := s.enqueueRunCommand(t.Context(), "run", extra); err == nil {
		t.Fatal("admitted work past priority fence")
	}
	run := drainCommandsForTest(t, s)
	if run.Commands[0].State != "refused" || run.Commands[1].State != "applied" || run.Tasks[0].State != marshal.HandedIn || run.State != marshal.AwaitingUser {
		t.Fatalf("priority result: %+v", run)
	}
	launched, err := s.dispatchReady(t.Context(), "run", run, marshalBrief)
	if err != nil || len(launched) != 0 {
		t.Fatalf("paused dispatch=%v %v", launched, err)
	}
	// A priority stop must never overwrite an existing permanent escalation pause.
	s2 := handedInFixture(t)
	escRun, rev, err := s2.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	pauseMarshal(&escRun, marshal.Pause{Reason: "security hold", Code: marshal.CodeSecurity, Action: marshal.PauseUsePlan})
	if err = s2.save(t.Context(), "run", escRun, rev); err != nil {
		t.Fatal(err)
	}
	stopCmd := runCommandForTest(t, s2, "stop-perm", "stop", "")
	if _, err := s2.enqueueRunCommand(t.Context(), "run", stopCmd); err != nil {
		t.Fatal(err)
	}
	after, err := s2.Snapshot(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if after.State != marshal.AwaitingUser || after.Pause == nil || after.Pause.Code != marshal.CodeSecurity {
		t.Fatalf("priority stop erased permanent escalation pause: %+v", after.Pause)
	}
}

func TestRunCommandsUseSingleWriter(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	s, _ := marshalFixture(t, 2)
	fake := s.Model.(marshalFakeModel)
	draft := fake.draft
	draft.Tasks[1].DependsOn = nil
	draft.Plan.Tasks[1].DependsOn = nil
	graph, err := plan.BuildGraph(draft.Plan.Tasks)
	if err != nil {
		t.Fatal(err)
	}
	draft.Plan.Graph = graph
	fake.draft = draft
	s.Model = fake
	if _, err := s.StartPlanning(ctx, "run", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	a, err := s.Dispatch(ctx, "run", "a", "write")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CollectHandIn(ctx, "run", a); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	s.Drivers["worker"] = driver.Governed{Provider: "worker", Run: func(ctx context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return nil, os.WriteFile(filepath.Join(req.Worktree, req.Task.PlanTaskID+".txt"), []byte("done\n"), 0600)
	}}
	b, err := s.Dispatch(ctx, "run", "b", "write")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	s.markDispatchLive("run", []MarshalDispatch{b})
	w := s.runWriter("run")
	done := make(chan error, 1)
	ready := make(chan struct{})
	go func() {
		if _, ok := w.startExecution("active-test"); !ok {
			done <- errors.New("writer already active")
			return
		}
		w.mu.Lock()
		close(ready)
		defer func() { w.endExecution(); w.mu.Unlock() }()
		writerCtx := context.WithValue(ctx, runWriterContextKey{}, w)
		current := func() (marshal.Run, error) { return s.Snapshot(ctx, "run") }
		done <- s.collectAll(writerCtx, "run", []MarshalDispatch{b}, nil, current)
	}()
	<-ready
	// Acquiring this shared writer waits until collection releases it to wait
	// for the unrelated live worker, rather than racing a state write.
	c := runCommandForTest(t, s, "live-return", "return", "a")
	receipt, err := s.enqueueRunCommand(ctx, "run", c)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.State != "queued" {
		t.Fatalf("receipt=%+v", receipt)
	}
	for {
		snapshot, err := s.Snapshot(ctx, "run")
		if err != nil {
			t.Fatal(err)
		}
		if len(snapshot.Commands) > 0 && snapshot.Commands[0].State == "applied" {
			if snapshot.Tasks[1].State != marshal.Dispatched {
				t.Fatal("unrelated worker was collected before return")
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("return did not apply while worker was live")
		case <-time.After(time.Millisecond):
		}
	}
	if id := s.ActiveExecution("run"); id != "active-test" {
		t.Fatalf("execution=%q", id)
	}
	resumed, err := s.Resume(ctx, "run")
	if err != nil || resumed.ExecutionID != "active-test" {
		t.Fatalf("resume while running: execution=%q err=%v", resumed.ExecutionID, err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	run := drainCommandsForTest(t, s)
	if taskReturns(run.Tasks[0]) != 1 || run.Tasks[1].State != marshal.HandedIn {
		t.Fatalf("writer ordering: %s", fmt.Sprint(run.Tasks))
	}
}

func TestCommandTypedAgainstOlderRevisionIsQueued(t *testing.T) {
	s := handedInFixture(t)
	c := runCommandForTest(t, s, "late-return", "return", "a")
	run, rev, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	// Execution saved the run after the owner read it (the revision moves).
	if err = s.save(t.Context(), "run", run, rev); err != nil {
		t.Fatal(err)
	}
	receipt := queueRunCommandForTest(t, s, c)
	if receipt.State != "queued" {
		t.Fatalf("command against an older run revision was not queued: %+v", receipt)
	}
}

func TestRefusedPriorityStopClearsItsFence(t *testing.T) {
	s := handedInFixture(t)
	stop := runCommandForTest(t, s, "stop", "stop", "")
	queueRunCommandForTest(t, s, stop)
	run, rev, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	// The approved plan changes before the stop is drained, so it is refused.
	run.PlanVersion++
	if err = s.save(t.Context(), "run", run, rev); err != nil {
		t.Fatal(err)
	}
	run = drainCommandsForTest(t, s)
	if run.Commands[0].State != "refused" {
		t.Fatalf("stop was not refused: %+v", run.Commands[0])
	}
	if run.CommandFence != "" {
		t.Fatalf("refused stop left new starts fenced: %q", run.CommandFence)
	}
}

func TestQueuedRetryRetainsFreshSession(t *testing.T) {
	s := handedInFixture(t)
	s.Model = nil
	run, rev, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	run.Tasks[0].State = marshal.Escalated
	if err = s.save(t.Context(), "run", run, rev); err != nil {
		t.Fatal(err)
	}
	c := runCommandForTest(t, s, "retry-task", "retry", "a")
	c.FreshSession = true
	receipt := queueRunCommandForTest(t, s, c)
	before, err := s.Snapshot(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if receipt.State != "queued" || before.Tasks[0].State != marshal.Escalated {
		t.Fatalf("retry applied before drain: %+v", receipt)
	}
	after := drainCommandsForTest(t, s)
	if after.Commands[0].State != "applied" || after.Tasks[0].State != marshal.Returned || after.Tasks[0].FreshSessionRetries != 1 {
		t.Fatalf("retry did not retain its session choice: %+v", after)
	}
	after = drainCommandsForTest(t, s)
	if after.Tasks[0].FreshSessionRetries != 1 {
		t.Fatal("retry was replayed")
	}
}

func TestLiveDispatchesStayWithinRepository(t *testing.T) {
	first, _ := marshalFixture(t, 1)
	second, _ := marshalFixture(t, 1)
	first.markDispatchLive("run", []MarshalDispatch{{TaskID: "a"}})
	t.Cleanup(func() { first.clearDispatchLive("run", "a") })
	if !first.dispatchLive("run", "a") || second.dispatchLive("run", "a") {
		t.Fatal("live worker registry crossed repository boundaries")
	}
	second.clearDispatchLive("run", "a")
	if !first.dispatchLive("run", "a") {
		t.Fatal("another repository cleared a live worker")
	}
}
