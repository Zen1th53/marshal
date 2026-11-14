package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/statepath"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/verification"
)

func mergedRecoveryFixture(t *testing.T) *MarshalService {
	t.Helper()
	s, _ := marshalFixture(t, 1)
	if _, err := s.StartPlanning(t.Context(), "run", "write", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(t.Context(), "run"); err != nil {
		t.Fatal(err)
	}
	d, err := s.Dispatch(t.Context(), "run", "a", "write")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CollectHandIn(t.Context(), "run", d); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Review(t.Context(), "run", "a", knownCharge()); err != nil {
		t.Fatal(err)
	}
	if err = s.Merge(t.Context(), "run", "a"); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestResumeVerificationPauseAndBudgetRefusal(t *testing.T) {
	s := mergedRecoveryFixture(t)
	verify := s.Verify
	s.Verify = func(ctx context.Context, r marshal.Run, h string) (verification.Session, verification.Binding, error) {
		session, binding, err := verify(ctx, r, h)
		session.RequiredChecks["integration"] = verification.StatusFail
		return session, binding, err
	}
	if _, err := s.VerifyMerged(t.Context(), "run", knownCharge()); err != nil {
		t.Fatal(err)
	}
	run, err := s.Resume(t.Context(), "run")
	if err != nil || run.State != marshal.Verifying {
		t.Fatalf("resume verification: %s %v", run.State, err)
	}
	s.Verify = verify
	if _, err = s.VerifyMerged(t.Context(), "run", knownCharge()); err != nil {
		t.Fatal(err)
	}
	run, rev, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	run.Budget.Tokens.Plan = 1
	if err = s.save(t.Context(), "run", run, rev); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Charge(t.Context(), "run", "", "verification", knownCharge()); err != nil {
		t.Fatal(err)
	}
	run, err = s.Resume(t.Context(), "run")
	if err == nil || !strings.Contains(err.Error(), "usage unknown: tokens") || run.State != marshal.AwaitingUser {
		t.Fatalf("budget resume: %s %v", run.State, err)
	}
	// A run with known usage reports the actual exceeded resource instead.
	known, _ := marshalFixture(t, 1)
	if _, err = known.StartPlanning(t.Context(), "known", "write", marshal.Budget{Tokens: marshal.Ceiling{Plan: 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err = known.Charge(t.Context(), "known", "", "planning", marshal.Charge{Tokens: marshal.Amount{Value: 2, Known: true}, Money: marshal.Amount{Known: true}}); err != nil {
		t.Fatal(err)
	}
	blocked, err := known.Resume(t.Context(), "known")
	if err == nil || !strings.Contains(err.Error(), "plan budget exceeded: tokens") || blocked.State != marshal.AwaitingUser {
		t.Fatalf("known budget resume: %s %v", blocked.State, err)
	}
}

func TestMajorAmendmentDispatchKeepsOldArtifacts(t *testing.T) {
	s := mergedRecoveryFixture(t)
	old, _, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	oldDir := filepath.Join(s.Worktrees, worktreeTaskID("run", "a"))
	oldHead := marshalGit(t, oldDir, "rev-parse", "HEAD")
	oldEvidence, err := s.Store.GetMarshalHandIn(t.Context(), "run", "a", 1)
	if err != nil {
		t.Fatal(err)
	}
	// The amendment changes merged task a, so its work is redone on a new
	// artifact revision; unchanged merged tasks would keep their state.
	d := cloneMarshalDraft(s.Model.(marshalFakeModel).draft)
	d.Plan.Budget.MaxTasks = 100
	d.Tasks[0].Instructions = "write a.txt again"
	d.Plan.Tasks[0].Instructions = d.Tasks[0].Instructions
	run, err := s.ApplyAmendDraft(t.Context(), "run", "larger budget", d)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != marshal.Drafting {
		t.Fatal(run.State)
	}
	if run.Tasks[0].Branch == old.Tasks[0].Branch {
		t.Fatal("revision reused task branch")
	}
	if _, err = s.Approve(t.Context(), "run"); err != nil {
		t.Fatal(err)
	}
	dispatch, err := s.Dispatch(t.Context(), "run", "a", "write again")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CollectHandIn(t.Context(), "run", dispatch); err != nil {
		t.Fatal(err)
	}
	if marshalGit(t, oldDir, "rev-parse", "HEAD") != oldHead {
		t.Fatal("old evidence changed")
	}
	if _, err = os.Stat(filepath.Join(oldDir, "a.txt")); errors.Is(err, os.ErrNotExist) {
		t.Fatal("old worktree removed")
	}
	if _, err = s.Review(t.Context(), "run", "a", knownCharge()); err != nil {
		t.Fatal(err)
	}
	if err = s.Merge(t.Context(), "run", "a"); err != nil {
		t.Fatal(err)
	}
	archived, err := s.Store.GetMarshalHandIn(t.Context(), "run", "a", 1)
	if err != nil || archived.Value.ResultCommit != oldEvidence.Value.ResultCommit {
		t.Fatalf("old evidence replaced: %+v %v", archived, err)
	}
	if _, err = s.Store.GetMarshalHandIn(t.Context(), "run", "a", run.Tasks[0].EvidenceAttemptBase+1); err != nil {
		t.Fatal(err)
	}
}

func TestLifecycleEffectRecovery(t *testing.T) {
	for _, phase := range []string{"launch", "hand-in", "merge", "close"} {
		t.Run(phase, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "state.db")
			s, repo := marshalFixtureAt(t, 1, dbPath)
			ctx := t.Context()
			if _, err := s.StartPlanning(ctx, "run", "write", marshal.Budget{}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Approve(ctx, "run"); err != nil {
				t.Fatal(err)
			}
			interrupted := errors.New("injected interruption after effect")
			s.AfterLifecycleEffect = func(kind, id string) error {
				if kind == phase {
					return interrupted
				}
				return nil
			}
			d, err := s.Dispatch(ctx, "run", "a", "write")
			if phase == "launch" {
				if !errors.Is(err, interrupted) {
					t.Fatalf("fault not reached: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				_, err = s.CollectHandIn(ctx, "run", d)
				if phase == "hand-in" {
					if !errors.Is(err, interrupted) {
						t.Fatalf("fault not reached: %v", err)
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					if _, err = s.Review(ctx, "run", "a", knownCharge()); err != nil {
						t.Fatal(err)
					}
					err = s.Merge(ctx, "run", "a")
					if phase == "merge" {
						if !errors.Is(err, interrupted) {
							t.Fatalf("fault not reached: %v", err)
						}
					} else {
						if err != nil {
							t.Fatal(err)
						}
						err = s.Close(ctx, "run")
						if !errors.Is(err, interrupted) {
							t.Fatalf("fault not reached: %v", err)
						}
					}
				}
			}
			// Recreate the service around a reopened database, so recovery cannot use
			// an in-memory operation or driver handle.
			if err = s.Store.Close(); err != nil {
				t.Fatal(err)
			}
			db, err := store.Open(ctx, dbPath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			restarted := *s
			restarted.Store = db
			restarted.AfterLifecycleEffect = nil
			if err = restarted.RecoverPendingOperations(ctx); err != nil {
				t.Fatal(err)
			}
			before := marshalGit(t, repo, "rev-list", "--all", "--count")
			run, err := restarted.Resume(ctx, "run")
			if err != nil {
				t.Fatal(err)
			}
			switch phase {
			case "launch":
				if run.Tasks[0].State != marshal.Returned {
					t.Fatalf("launch recovery: %+v", run)
				}
			case "hand-in":
				if run.Tasks[0].State != marshal.HandedIn {
					t.Fatalf("hand-in recovery: %+v", run)
				}
			case "merge":
				if run.State != marshal.Verifying || run.Tasks[0].State != marshal.Merged {
					t.Fatalf("merge recovery: %+v", run)
				}
			case "close":
				if run.State != marshal.Closed {
					t.Fatalf("close recovery: %+v", run)
				}
			}
			if after := marshalGit(t, repo, "rev-list", "--all", "--count"); after != before {
				t.Fatalf("recovery repeated Git effect: %s -> %s", before, after)
			}
			if run.Operation != nil {
				t.Fatal("unfinished operation after recovery")
			}
			history, err := db.MarshalDecisions(ctx, "run")
			if err != nil {
				t.Fatal(err)
			}
			completions := 0
			for _, event := range history {
				if event.Data["operation_id"] != nil {
					completions++
				}
			}
			if completions == 0 {
				t.Fatal("no durable operation completion")
			}
		})
	}
}

func TestRecoveryCannotCloseBeforeAuthorization(t *testing.T) {
	s, repo := marshalFixture(t, 1)
	if _, err := s.StartPlanning(t.Context(), "run", "write", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	run, rev, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	next := run
	next.State = marshal.Closed
	head := marshalGit(t, repo, "rev-parse", "HEAD")
	op := &marshal.LifecycleOperation{Kind: "close", Before: head, After: head, Target: "refs/heads/main", Next: &next, Event: events.Event{Type: events.EventTypeMarshalRunClosed}}
	if _, err = s.beginOperation(t.Context(), "run", run, rev, op); err != nil {
		t.Fatal(err)
	}
	if err = s.RecoverPendingOperations(t.Context()); err != nil {
		t.Fatal(err)
	}
	run, _, err = s.load(t.Context(), "run")
	if err != nil || run.State == marshal.Closed {
		t.Fatalf("unauthorized close recovered: %+v %v", run, err)
	}
}

func TestSecurityPauseReportsResolution(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	if _, err := s.StartPlanning(t.Context(), "run", "write", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Escalate(t.Context(), "run", "", marshal.CodeEscalated, "security suspension"); err != nil {
		t.Fatal(err)
	}
	run, err := s.Resume(t.Context(), "run")
	if err == nil || !strings.Contains(err.Error(), "security suspension") || !strings.Contains(err.Error(), "next step") || run.State != marshal.AwaitingUser {
		t.Fatalf("false security resume: %+v %v", run, err)
	}
}

func TestRecoveryBeforeMergeWorktreePreparationKeepsAmendmentAvailable(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	if _, err := s.StartPlanning(t.Context(), "run", "write", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	run, rev, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	next := run
	next.State = marshal.Verifying
	op := &marshal.LifecycleOperation{Kind: "merge", TaskID: "a", Dir: filepath.Join(s.Worktrees, "not-prepared"), Target: "refs/heads/" + statepath.RefName + "/run/integration", Before: run.BaseCommit, After: run.BaseCommit, Next: &next, Event: events.Event{Type: events.EventTypeMarshalTaskMerged}}
	if _, err = s.beginOperation(t.Context(), "run", run, rev, op); err != nil {
		t.Fatal(err)
	}
	if err = s.RecoverPendingOperations(t.Context()); err != nil {
		t.Fatal(err)
	}
	run, _, err = s.load(t.Context(), "run")
	if err != nil || run.Operation != nil || run.State != marshal.AwaitingUser {
		t.Fatalf("intent without effect blocks amendment: %+v %v", run, err)
	}
	if err = unfinishedMarshalOperation(run); err != nil {
		t.Fatal(err)
	}
}

func TestMarshalOversizedEventRecoveryOpen(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	rt, err := Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	s := rt.Marshal()
	head := marshalGit(t, repo.Path(), "rev-parse", "HEAD")
	refs := marshalGit(t, repo.Path(), "show-ref")
	event, err := s.decisionEvent("oversized", "a", events.EventTypeMarshalTaskDispatched, map[string]any{"brief": oversizedEventBrief(t), "operation_id": "old-launch", "operation_kind": "launch"})
	if err != nil {
		t.Fatal(err)
	}
	event.ID = "old-launch"
	if !errors.Is(event.Validate(), events.ErrEventDataInvalid) {
		t.Fatal("expected oversized pending event")
	}
	run := marshal.Run{PlanID: "oversized-plan", PlanVersion: 1, Settings: marshal.DefaultSettings(), State: marshal.Approved, Tasks: []marshal.Task{{PlanTaskID: "a", State: marshal.Queued}}}
	next := run
	next.Tasks = append([]marshal.Task(nil), run.Tasks...)
	next.Tasks[0].State = marshal.Dispatched
	next.State = marshal.Dispatching
	run.Operation = &marshal.LifecycleOperation{ID: event.ID, Kind: "launch", TaskID: "a", Next: &next, Event: event}
	if err := s.record(t.Context(), "oversized", "a", events.EventTypeMarshalEscalated, map[string]any{"reason": "prior history"}); err != nil {
		t.Fatal(err)
	}
	history, err := s.Store.MarshalDecisions(t.Context(), "oversized")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.SetMarshalRun(t.Context(), s.ProjectID, "oversized", run, 0); err != nil {
		t.Fatal(err)
	}
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	rt, err = Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatalf("oversized pending launch blocks app.Open: %v", err)
	}
	defer rt.Close()
	s = rt.Marshal()
	recovered, _, err := s.load(t.Context(), "oversized")
	if err != nil || recovered.Operation != nil || recovered.Tasks[0].ResultCommit != "" || recovered.Tasks[0].State != marshal.Dispatched || recovered.State != marshal.Dispatching {
		t.Fatalf("recovery invented completion: %+v %v", recovered, err)
	}
	if _, err := s.Store.GetMarshalHandIn(t.Context(), "oversized", "a", 1); err == nil {
		t.Fatal("recovery fabricated a hand-in")
	}
	after, err := s.Store.MarshalDecisions(t.Context(), "oversized")
	if err != nil || len(after) != len(history)+1 {
		t.Fatalf("history length: %d %v", len(after), err)
	}
	if !reflect.DeepEqual(after[:len(history)], history) {
		t.Fatal("prior event history changed")
	}
	full, err := s.fullMarshalEvent(after[len(after)-1])
	if err != nil || full.Data["brief"] != event.Data["brief"] {
		t.Fatalf("recovery lost full event: %v", err)
	}
	if after[len(after)-1].Data["launch_outcome"] != "interrupted; driver completion unknown" {
		t.Fatal("recovery advertised a proven launch")
	}
	if err := after[len(after)-1].Validate(); err != nil {
		t.Fatal(err)
	}
	if err := s.RecoverPendingOperations(t.Context()); err != nil {
		t.Fatal(err)
	}
	again, err := s.Store.MarshalDecisions(t.Context(), "oversized")
	if err != nil || len(again) != len(after) {
		t.Fatal("recovery is not idempotent")
	}
	if marshalGit(t, repo.Path(), "rev-parse", "HEAD") != head || marshalGit(t, repo.Path(), "show-ref") != refs {
		t.Fatal("recovery changed Git history")
	}
}

func TestMarshalOversizedEventRecoveryInvalidReference(t *testing.T) {
	for _, kind := range []string{"missing", "tampered", "permissions"} {
		t.Run(kind, func(t *testing.T) {
			repo := runtimeRepo(t)
			if _, err := Bootstrap(t.Context(), repo.Path()); err != nil {
				t.Fatal(err)
			}
			rt, err := Open(t.Context(), repo.Path())
			if err != nil {
				t.Fatal(err)
			}
			defer rt.Close()
			s := rt.Marshal()
			event, err := s.decisionEvent("bad", "a", events.EventTypeMarshalTaskDispatched, map[string]any{"brief": oversizedEventBrief(t)})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.boundMarshalEvent(&event); err != nil {
				t.Fatal(err)
			}
			run := marshal.Run{PlanID: "bad-plan", PlanVersion: 1, Settings: marshal.DefaultSettings(), State: marshal.Approved, Tasks: []marshal.Task{{PlanTaskID: "a", State: marshal.Queued}}, Operation: &marshal.LifecycleOperation{ID: event.ID, Kind: "launch", TaskID: "a", Event: event}}
			if _, err := s.Store.SetMarshalRun(t.Context(), s.ProjectID, "bad", run, 0); err != nil {
				t.Fatal(err)
			}
			before, err := s.Store.GetMarshalRun(t.Context(), s.ProjectID, "bad")
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(filepath.Dir(s.Worktrees), "event-artifacts", artifactReferenceName(t, event))
			switch kind {
			case "missing":
				err = os.Remove(path)
			case "tampered":
				err = os.Chmod(path, 0600)
				if err == nil {
					err = os.WriteFile(path, []byte("tampered"), 0600)
				}
				if err == nil {
					err = os.Chmod(path, 0400)
				}
			case "permissions":
				err = os.Chmod(path, 0444)
			}
			if err != nil {
				t.Fatal(err)
			}
			databasePath := rt.layout.Database
			if err := rt.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(t.Context(), repo.Path())
			if reopened != nil {
				reopened.Close()
			}
			if !errors.Is(err, errMarshalEventArtifact) {
				t.Fatalf("startup hid unsafe reference: %v", err)
			}
			db, err := store.Open(t.Context(), databasePath)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			after, err := db.GetMarshalRun(t.Context(), s.ProjectID, "bad")
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("failed recovery changed intent: %v", err)
			}
			history, err := db.MarshalDecisions(t.Context(), "bad")
			if err != nil || len(history) != 0 {
				t.Fatalf("failed recovery wrote history: %v", err)
			}
		})
	}
}

func TestMarshalOversizedEventRecoveryInterruptedDispatch(t *testing.T) {
	for _, expanded := range []bool{false, true} {
		t.Run(fmt.Sprint(expanded), func(t *testing.T) {
			databasePath := filepath.Join(t.TempDir(), "state.db")
			s, _ := marshalFixtureAt(t, 1, databasePath)
			if err := os.Chmod(filepath.Dir(s.Worktrees), 0700); err != nil {
				t.Fatal(err)
			}
			if _, err := s.StartPlanning(t.Context(), "run", "write", marshal.Budget{}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Approve(t.Context(), "run"); err != nil {
				t.Fatal(err)
			}
			if expanded {
				s.ProjectContext = func(context.Context, string, string) (string, error) { return oversizedEventBrief(t), nil }
			}
			launches := 0
			s.Drivers["worker"] = oversizedEventDriver{Driver: s.Drivers["worker"], launch: func(driver.Request) error { launches++; return nil }}
			interrupted := errors.New("interrupted after launch")
			s.AfterLifecycleEffect = func(string, string) error { return interrupted }
			if _, err := s.Dispatch(t.Context(), "run", "a", oversizedEventBrief(t)); !errors.Is(err, interrupted) {
				t.Fatal(err)
			}
			if launches != 1 {
				t.Fatal("launch count", launches)
			}
			if err := s.Store.Close(); err != nil {
				t.Fatal(err)
			}
			db, err := store.Open(t.Context(), databasePath)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			restarted := *s
			restarted.Store = db
			restarted.AfterLifecycleEffect = nil
			if err := restarted.RecoverPendingOperations(t.Context()); err != nil {
				t.Fatal(err)
			}
			run, _, err := restarted.load(t.Context(), "run")
			if err != nil || run.Operation != nil || run.State != marshal.Dispatching || run.Tasks[0].State != marshal.Dispatched || run.Tasks[0].ResultCommit != "" || launches != 1 {
				t.Fatalf("incorrect recovery: %+v %v", run, err)
			}
			if _, err := db.GetMarshalHandIn(t.Context(), "run", "a", 1); err == nil {
				t.Fatal("recovery fabricated a hand-in")
			}
		})
	}
}

func TestMarshalOversizedEventRecoveryLaunchContextFailure(t *testing.T) {
	for _, launchErr := range []error{context.DeadlineExceeded, context.Canceled} {
		for _, oversized := range []bool{false, true} {
			t.Run(fmt.Sprintf("%v/oversized=%v", launchErr, oversized), func(t *testing.T) {
				s := oversizedEventFixture(t)
				history, err := s.Store.MarshalDecisions(t.Context(), "run")
				if err != nil {
					t.Fatal(err)
				}
				launches := 0
				var delivered string
				s.Drivers["worker"] = oversizedEventDriver{Driver: s.Drivers["worker"], launch: func(req driver.Request) error {
					launches++
					// Delivery can precede a canceled or timed-out acknowledgement.
					delivered = filepath.Join(req.Worktree, "delivered.txt")
					if err := os.WriteFile(delivered, []byte(req.Brief), 0600); err != nil {
						t.Fatal(err)
					}
					return fmt.Errorf("launch acknowledgement: %w", launchErr)
				}}
				brief := "write"
				if oversized {
					brief = oversizedEventBrief(t)
				}
				if _, err := s.Dispatch(t.Context(), "run", "a", brief); !errors.Is(err, launchErr) {
					t.Fatalf("lost launch context error: %v", err)
				}
				run, _, err := s.load(t.Context(), "run")
				if err != nil || run.Operation == nil || run.Operation.Kind != "launch" {
					t.Fatalf("possibly delivered launch was abandoned: %+v %v", run, err)
				}
				if err := unfinishedMarshalOperation(run); err == nil {
					t.Fatal("uncertain launch permits retry before reconciliation")
				}
				after, err := s.Store.MarshalDecisions(t.Context(), "run")
				if err != nil || !reflect.DeepEqual(after, history) {
					t.Fatalf("uncertain launch recorded a definite outcome: %v", err)
				}
				if err := s.RecoverPendingOperations(t.Context()); err != nil {
					t.Fatal(err)
				}
				run, _, err = s.load(t.Context(), "run")
				if err != nil || run.Operation != nil || run.State != marshal.Dispatching || run.Tasks[0].State != marshal.Dispatched || run.Tasks[0].ResultCommit != "" {
					t.Fatalf("uncertain launch recovered incorrectly: %+v %v", run, err)
				}
				after, err = s.Store.MarshalDecisions(t.Context(), "run")
				if err != nil || len(after) != len(history)+1 {
					t.Fatalf("missing recovery decision: %v", err)
				}
				event := after[len(after)-1]
				if event.Data["launch_outcome"] != "interrupted; driver completion unknown" || event.Data["reason"] != nil {
					t.Fatalf("context failure misclassified as refusal: %+v", event)
				}
				full, err := s.fullMarshalEvent(event)
				if err != nil || full.Data["brief"] != brief {
					t.Fatalf("uncertain launch lost context: %v", err)
				}
				if _, err := s.Dispatch(t.Context(), "run", "a", brief); err == nil {
					t.Fatal("possibly delivered launch was dispatched again")
				}
				if err := s.RecoverPendingOperations(t.Context()); err != nil {
					t.Fatal(err)
				}
				again, err := s.Store.MarshalDecisions(t.Context(), "run")
				if err != nil || !reflect.DeepEqual(again, after) || launches != 1 {
					t.Fatalf("uncertain launch recovery is not idempotent: launches=%d err=%v", launches, err)
				}
				data, err := os.ReadFile(delivered)
				if err != nil || string(data) != brief {
					t.Fatalf("recovery changed delivered evidence: %v", err)
				}
				if _, err := s.Store.GetMarshalHandIn(t.Context(), "run", "a", 1); err == nil {
					t.Fatal("recovery fabricated a hand-in")
				}
			})
		}
	}
}

func TestMarshalOversizedEventRecoveryCompletionFailure(t *testing.T) {
	for _, phase := range []string{"after-effect", "complete"} {
		t.Run(phase, func(t *testing.T) {
			databasePath := filepath.Join(t.TempDir(), "state.db")
			s := oversizedEventFixture(t, databasePath)
			history, err := s.Store.MarshalDecisions(t.Context(), "run")
			if err != nil {
				t.Fatal(err)
			}
			launches, cancels := 0, 0
			s.Drivers["worker"] = oversizedEventDriver{Driver: s.Drivers["worker"], launch: func(driver.Request) error { launches++; return nil }, cancel: func(*driver.Handle) error { cancels++; return nil }}
			injected := errors.New("after-effect interruption")
			s.AfterLifecycleEffect = func(string, string) error {
				if phase == "after-effect" {
					return injected
				}
				// A competing state save invalidates completeOperation's CAS.
				// It retains a valid pending intent so reopen can reconcile it.
				record, err := s.Store.GetMarshalRun(t.Context(), s.ProjectID, "run")
				if err != nil {
					t.Fatal(err)
				}
				if err := s.save(t.Context(), "run", record.Value, record.Revision); err != nil {
					t.Fatal(err)
				}
				return nil
			}
			_, err = s.Dispatch(t.Context(), "run", "a", oversizedEventBrief(t))
			if err == nil || (phase == "after-effect" && !errors.Is(err, injected)) {
				t.Fatalf("expected completion failure: %v", err)
			}
			if launches != 1 || cancels != 1 {
				t.Fatalf("launches=%d cancels=%d", launches, cancels)
			}
			pending, _, err := s.load(t.Context(), "run")
			if err != nil || pending.Operation == nil {
				t.Fatalf("failed completion discarded intent: %v", err)
			}
			after, err := s.Store.MarshalDecisions(t.Context(), "run")
			if err != nil || !reflect.DeepEqual(history, after) {
				t.Fatalf("failed completion charged usage or wrote history: %v", err)
			}
			if err := s.Store.Close(); err != nil {
				t.Fatal(err)
			}
			db, err := store.Open(t.Context(), databasePath)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			restarted := *s
			restarted.Store, restarted.AfterLifecycleEffect = db, nil
			if err := restarted.RecoverPendingOperations(t.Context()); err != nil {
				t.Fatal(err)
			}
			run, _, err := restarted.load(t.Context(), "run")
			if err != nil || run.Operation != nil || run.Tasks[0].State != marshal.Dispatched || launches != 1 || cancels != 1 {
				t.Fatalf("incorrect recovery: %+v %v; launches=%d cancels=%d", run, err, launches, cancels)
			}
			after, err = db.MarshalDecisions(t.Context(), "run")
			if err != nil || len(after) != len(history)+1 || !reflect.DeepEqual(history, after[:len(history)]) {
				t.Fatalf("recovery changed prior history: %v", err)
			}
			event := after[len(history)]
			if event.Type != events.EventTypeMarshalTaskDispatched || event.Data["launch_outcome"] != "interrupted; driver completion unknown" || event.Data["charge_phase"] != nil {
				t.Fatalf("recovery invented effects/usage: %+v", event)
			}
			if _, err := db.GetMarshalHandIn(t.Context(), "run", "a", 1); err == nil {
				t.Fatal("recovery fabricated a hand-in")
			}
			if err := restarted.RecoverPendingOperations(t.Context()); err != nil {
				t.Fatal(err)
			}
			again, err := db.MarshalDecisions(t.Context(), "run")
			if err != nil || !reflect.DeepEqual(again, after) || launches != 1 {
				t.Fatalf("recovery repeated effects: %v", err)
			}
		})
	}
}

func TestMarshalOversizedEventRecoveryLegacyIsolation(t *testing.T) {
	for _, failure := range []string{"wrong-brief-digest", "read-only"} {
		t.Run(failure, func(t *testing.T) {
			databasePath := filepath.Join(t.TempDir(), "state.db")
			s := oversizedEventFixture(t, databasePath)
			base, _, err := s.load(t.Context(), "run")
			if err != nil {
				t.Fatal(err)
			}
			launches := 0
			s.Drivers["worker"] = oversizedEventDriver{Driver: s.Drivers["worker"], launch: func(driver.Request) error { launches++; return errors.New("recovery must not launch") }}
			for _, id := range []string{"a-bad", "z-good"} {
				brief := oversizedEventBrief(t)
				if failure == "read-only" && id == "z-good" {
					brief = "small independent intent"
				}
				sum := sha256.Sum256([]byte(brief))
				digest := hex.EncodeToString(sum[:])
				if failure == "wrong-brief-digest" && id == "a-bad" {
					digest = strings.Repeat("0", 64)
				}
				event, err := s.decisionEvent(id, "a", events.EventTypeMarshalTaskDispatched, map[string]any{"brief": brief, "brief_sha256": digest})
				if err != nil {
					t.Fatal(err)
				}
				run, next := base, base
				next.Tasks = append([]marshal.Task(nil), base.Tasks...)
				next.Tasks[0].State, next.State = marshal.Dispatched, marshal.Dispatching
				run.Operation = &marshal.LifecycleOperation{ID: event.ID, Kind: "launch", TaskID: "a", Event: event, Next: &next}
				if _, err := s.Store.SetMarshalRun(t.Context(), s.ProjectID, id, run, 0); err != nil {
					t.Fatal(err)
				}
			}
			badBefore, err := s.Store.GetMarshalRun(t.Context(), s.ProjectID, "a-bad")
			if err != nil {
				t.Fatal(err)
			}
			if failure == "read-only" {
				dir := filepath.Join(filepath.Dir(s.Worktrees), "event-artifacts")
				if err := os.Mkdir(dir, 0500); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
			}
			for reopen := 0; reopen < 2; reopen++ {
				if err := s.Store.Close(); err != nil {
					t.Fatal(err)
				}
				db, err := store.Open(t.Context(), databasePath)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = db.Close() })
				s.Store = db
				if err := s.RecoverPendingOperations(t.Context()); !errors.Is(err, errMarshalEventArtifact) || !strings.Contains(err.Error(), "a-bad") {
					t.Fatalf("missing per-run failure: %v", err)
				}
				badAfter, err := db.GetMarshalRun(t.Context(), s.ProjectID, "a-bad")
				if err != nil || !reflect.DeepEqual(badBefore, badAfter) {
					t.Fatalf("invalid legacy intent changed: %v", err)
				}
				badHistory, err := db.MarshalDecisions(t.Context(), "a-bad")
				if err != nil || len(badHistory) != 0 {
					t.Fatalf("invalid legacy completion fabricated: %v", err)
				}
				good, _, err := s.load(t.Context(), "z-good")
				if err != nil || good.Operation != nil || good.Tasks[0].State != marshal.Dispatched || good.State != marshal.Dispatching {
					t.Fatalf("bad run blocked independent recovery: %+v %v", good, err)
				}
				history, err := db.MarshalDecisions(t.Context(), "z-good")
				if err != nil || len(history) != 1 || history[0].Data["launch_outcome"] != "interrupted; driver completion unknown" {
					t.Fatalf("independent recovery is not idempotent: %+v %v", history, err)
				}
				full, err := s.fullMarshalEvent(history[0])
				if err != nil || full.Data["brief_sha256"] == nil {
					t.Fatalf("independent recovery lost verified brief: %v", err)
				}
				if launches != 0 {
					t.Fatalf("reopen launched %d workers", launches)
				}
			}
		})
	}
}

func TestVerificationErrorsLeaveDurableActionablePause(t *testing.T) {
	for _, boundary := range []string{"proof", "source", "cancelled"} {
		t.Run(boundary, func(t *testing.T) {
			s := mergedRecoveryFixture(t)
			cause := errors.New("runtime proof: check output exceeds admitted bound")
			ctx := t.Context()
			if boundary == "source" {
				s.requiredIntegrationSource = func(context.Context, string, marshal.Run, string) error { return cause }
			} else {
				s.Verify = func(context.Context, marshal.Run, string) (verification.Session, verification.Binding, error) {
					return verification.Session{}, verification.Binding{}, cause
				}
			}
			if boundary == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				s.Verify = func(context.Context, marshal.Run, string) (verification.Session, verification.Binding, error) {
					cancel()
					return verification.Session{}, verification.Binding{}, cause
				}
			}
			if _, err := s.VerifyMerged(ctx, "run", knownCharge()); !errors.Is(err, cause) {
				t.Fatalf("verification: %v", err)
			}
			// A new service instance reads the persisted disposition, not local state.
			reopened := &MarshalService{Store: s.Store, ProjectID: s.ProjectID, Repository: s.Repository}
			run, err := reopened.Snapshot(t.Context(), "run")
			if err != nil {
				t.Fatal(err)
			}
			if run.State != marshal.AwaitingUser || run.Pause == nil || !strings.Contains(run.Pause.Reason, cause.Error()) || run.Pause.ResumeState != marshal.Verifying || !strings.Contains(strings.Join(run.Pause.Resolutions, " "), "/marshal resume") {
				t.Fatalf("lost verification stop: %+v", run)
			}
		})
	}
}

func TestCancelledScopeRequiresReviewedAmendment(t *testing.T) {
	for _, size := range []int{1, 3} {
		for cancelled := 0; cancelled < size; cancelled++ {
			t.Run(fmt.Sprintf("tasks-%d/cancel-%d", size, cancelled), func(t *testing.T) {
				s, _ := marshalFixture(t, size)
				if _, err := s.StartPlanning(t.Context(), "cancel", "write", marshal.Budget{}); err != nil {
					t.Fatal(err)
				}
				if _, err := s.Approve(t.Context(), "cancel"); err != nil {
					t.Fatal(err)
				}
				run, rev, err := s.load(t.Context(), "cancel")
				if err != nil {
					t.Fatal(err)
				}
				id := run.Tasks[cancelled].PlanTaskID
				run.Tasks[cancelled].State = marshal.Escalated
				run.Tasks[cancelled].EscalationReason = "worker unavailable"
				if err := s.save(t.Context(), "cancel", run, rev); err != nil {
					t.Fatal(err)
				}
				if err := s.CancelTask(t.Context(), "cancel", id); err != nil {
					t.Fatal(err)
				}
				run, err = s.Execute(t.Context(), "cancel", func(marshal.Task, BriefContext) string { return "brief" }, nil)
				if err != nil {
					t.Fatal(err)
				}
				if run.State != marshal.AwaitingUser || run.Pause == nil || run.Pause.ResumeState != "" || !strings.Contains(run.Pause.Reason, id) {
					t.Fatalf("cancellation did not require review: %+v", run)
				}
				if _, err := s.Resume(t.Context(), "cancel"); err == nil || !strings.Contains(err.Error(), "amended plan") {
					t.Fatalf("resume bypassed scope review: %v", err)
				}
				for _, task := range run.Tasks {
					if task.State == marshal.Dispatched || task.State == marshal.Merged {
						t.Fatalf("execution continued past cancelled scope: %+v", task)
					}
				}
			})
		}
	}
}

func TestExecutableReplacementDoesNotChangeGoverningAdmission(t *testing.T) {
	s, repo := marshalFixture(t, 1)
	path := filepath.Join(repo, "marshal")
	if err := os.WriteFile(path, []byte("previous executable"), 0700); err != nil {
		t.Fatal(err)
	}
	startIntegrityRun(t, s, marshal.Budget{})
	if err := os.WriteFile(path, []byte("upgraded executable"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resume(t.Context(), "run"); err != nil {
		t.Fatalf("executable replacement misclassified as governing change: %v", err)
	}
}

func TestLegacyCancelledScopeCannotMergeVerifyOrClose(t *testing.T) {
	for _, boundary := range []string{"merge", "verify", "close"} {
		t.Run(boundary, func(t *testing.T) {
			s := mergedRecoveryFixture(t)
			run, rev, err := s.load(t.Context(), "run")
			if err != nil {
				t.Fatal(err)
			}
			run.Tasks[0].State = marshal.Cancelled
			// This is the historical runtime's completion disposition.
			run.State = marshal.Verifying
			if err := s.save(t.Context(), "run", run, rev); err != nil {
				t.Fatal(err)
			}
			invoked := false
			s.Verify = func(context.Context, marshal.Run, string) (verification.Session, verification.Binding, error) {
				invoked = true
				return verification.Session{}, verification.Binding{}, errors.New("must not verify cancelled scope")
			}
			switch boundary {
			case "merge":
				err = s.Merge(t.Context(), "run", run.Tasks[0].PlanTaskID)
			case "verify":
				_, err = s.VerifyMerged(t.Context(), "run", knownCharge())
			case "close":
				err = s.Close(t.Context(), "run")
			}
			if err == nil || !strings.Contains(err.Error(), "scope requires review") || invoked {
				t.Fatalf("cancelled scope passed %s: %v", boundary, err)
			}
			run, err = s.Snapshot(t.Context(), "run")
			if err != nil || run.Pause == nil || run.Pause.ResumeState != "" || run.State != marshal.AwaitingUser {
				t.Fatalf("scope stop not durable: %+v %v", run, err)
			}
		})
	}
}

// The merge commit lands in the owner's history, so it carries no internal
// operation identifier; recovery proves the merge from its parents instead.
func TestMergeCommitCarriesNoOperationID(t *testing.T) {
	s := mergedRecoveryFixture(t)
	messages := marshalGit(t, s.Repository, "log", "--all", "--format=%B")
	for _, internal := range []string{"Marshal-Operation", "marshal-op-"} {
		if strings.Contains(messages, internal) {
			t.Fatalf("merge commit message exposes %q:\n%s", internal, messages)
		}
	}
	if !strings.Contains(messages, "marshal: merge a") {
		t.Fatalf("merge commit missing:\n%s", messages)
	}
}
