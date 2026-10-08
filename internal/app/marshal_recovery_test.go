package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
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
	d := s.Model.(marshalFakeModel).draft
	d.Plan.Budget.MaxTasks = 100
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
	if err := s.Escalate(t.Context(), "run", "", "security suspension"); err != nil {
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
	op := &marshal.LifecycleOperation{Kind: "merge", TaskID: "a", Dir: filepath.Join(s.Worktrees, "not-prepared"), Target: "refs/heads/marshal/run/integration", Before: run.BaseCommit, After: run.BaseCommit, Next: &next, Event: events.Event{Type: events.EventTypeMarshalTaskMerged}}
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
