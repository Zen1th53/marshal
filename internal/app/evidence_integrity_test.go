package app

import (
	"context"
	"errors"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/verification"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReportDoesNotHideFailedApprovedCheck(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	startIntegrityRun(t, s, marshal.Budget{})
	run, _, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	c := run.Tasks[0].Checks[0]
	_, err = s.Store.SetMarshalHandIn(t.Context(), "run", "a", 1, marshal.HandIn{ResultCommit: "result", CheckResults: []marshal.CheckResult{{Command: c.Command, Criteria: c.Criteria, ResultCommit: "result", Passed: true}, {Command: c.Command, Criteria: c.Criteria, ResultCommit: "result", Passed: false}}})
	if err != nil {
		t.Fatal(err)
	}
	report, err := s.CompletionReport(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if report.Criteria[0].Status != "failed" {
		t.Fatalf("%+v", report)
	}
}

func TestCrossReviewReferencesMustResolve(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	startIntegrityRun(t, s, marshal.Budget{})
	d, err := s.Dispatch(t.Context(), "run", "a", "write")
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.CollectHandIn(t.Context(), "run", d)
	if err != nil {
		t.Fatal(err)
	}
	if err := resolveReviewReferences(marshal.Review{EvidenceRefs: []string{"invented"}}, h); err == nil {
		t.Fatal("invented evidence accepted")
	}
	if err := resolveReviewReferences(marshal.Review{EvidenceRefs: []string{"result:" + h.ResultCommit, "diff", "check:" + h.CheckResults[0].Command, "file:a.txt"}}, h); err != nil {
		t.Fatal(err)
	}
}

func TestHoneypotScanMissingAndQuarantineSurviveRestart(t *testing.T) {
	for _, outcome := range []string{"missing", "hit", "clean", "wrong-scan", "legacy-missing"} {
		t.Run(outcome, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "state.db")
			s, _ := marshalFixture(t, 1, dbPath)
			startIntegrityRun(t, s, marshal.Budget{})
			run, rev, err := s.load(t.Context(), "run")
			if err != nil {
				t.Fatal(err)
			}
			run.Tasks[0].State = marshal.Accepted
			run.Tasks[0].ResultCommit = run.BaseCommit
			run.Tasks[0].HoneypotRequired = true
			run.Tasks[0].HoneypotScanID = "scan-1"
			if outcome == "legacy-missing" {
				run.Tasks[0].HoneypotRequired = false
			}
			if err := s.save(t.Context(), "run", run, rev); err != nil {
				t.Fatal(err)
			}
			scanID, disposition := "scan-1", outcome
			if outcome == "wrong-scan" {
				scanID, disposition = "different-scan", "clean"
			}
			if outcome != "missing" && outcome != "legacy-missing" {
				id, err := model.NewID("EVENT-")
				if err != nil {
					t.Fatal(err)
				}
				if err := s.Store.AppendEvent(t.Context(), nil, model.Event{ID: id, Type: "HONEYPOT_SCAN", Timestamp: time.Now().UTC(), ProjectID: s.ProjectID, Data: map[string]any{"result_commit": run.BaseCommit, "scan_id": scanID, "outcome": disposition}}); err != nil {
					t.Fatal(err)
				}
			}
			// Close and reopen SQLite; no traps or process-local guard survive.
			if err := s.Store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := store.Open(t.Context(), dbPath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reopened.Close() })
			restarted := &MarshalService{Store: reopened, Repository: s.Repository, Worktrees: s.Worktrees, ProjectID: s.ProjectID}
			if outcome == "legacy-missing" {
				restarted.HandInGuard = func(context.Context, string, string, marshal.HandIn) (string, error) { return "", nil }
			}
			err = restarted.Merge(context.Background(), "run", "a")
			if outcome == "clean" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "honeypot") {
				t.Fatalf("quarantine bypass: %v", err)
			}
		})
	}
}

func TestVerifierFindingsPersistInCompletionReport(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	startIntegrityRun(t, s, marshal.Budget{})
	run, err := s.Snapshot(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	s.IndependentVerify = func(_ context.Context, run marshal.Run, head string, session verification.Session) (marshal.VerifierEvidence, error) {
		return marshal.VerifierEvidence{Reviewer: "verifier:test", Provider: "test", Commit: head, Verdict: "pass", Findings: []string{"boundary reviewed"}, InputDigest: verifierInputDigest(run, head, session)}, nil
	}
	if err := s.captureIndependentVerification(t.Context(), "run", run, run.BaseCommit, verification.Session{}); err != nil {
		t.Fatal(err)
	}
	// Reconstruct service to read only the durable evidence.
	restarted := &MarshalService{Store: s.Store, Repository: s.Repository, Worktrees: s.Worktrees, ProjectID: s.ProjectID}
	report, err := restarted.CompletionReport(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Verifiers) != 1 || len(report.Verifiers[0].Findings) != 1 || report.Verifiers[0].Findings[0] != "boundary reviewed" {
		t.Fatalf("findings lost: %+v", report)
	}
}

func TestNoChangeAcceptanceByTaskType(t *testing.T) {
	for _, kind := range []marshal.TaskType{marshal.TaskChange, marshal.TaskInspection, marshal.TaskVerification} {
		t.Run(string(kind), func(t *testing.T) {
			s, _ := marshalFixture(t, 1)
			draft := s.Model.(marshalFakeModel).draft
			draft.Tasks[0].Type = kind
			draft.Plan.Tasks[0].Type = string(kind)
			draft.Plan.Tasks[0].Mutating = kind == marshal.TaskChange
			draft.Tasks[0].Checks[0].Command = "test -f README.md"
			draft.Plan.Checks["a"] = []string{"test -f README.md"}
			draft.Plan.Graph, _ = plan.BuildGraph(draft.Plan.Tasks)
			s.Model = marshalFakeModel{draft: draft, review: marshal.Review{Verdict: marshal.VerdictAccept}}
			s.Drivers["worker"] = driver.Governed{Run: func(context.Context, driver.Request) ([]marshal.CommandRecord, error) { return nil, nil }}
			startIntegrityRun(t, s, marshal.Budget{})
			dispatch, err := s.Dispatch(t.Context(), "run", "a", "inspect")
			if err != nil {
				t.Fatal(err)
			}
			h, err := s.CollectHandIn(t.Context(), "run", dispatch)
			if err != nil {
				t.Fatal(err)
			}
			if h.ResultCommit != h.BaseCommit {
				t.Fatal("fixture unexpectedly changed")
			}
			verdict, err := s.Review(t.Context(), "run", "a", knownCharge())
			if kind == marshal.TaskChange {
				if err == nil && verdict == marshal.VerdictAccept {
					t.Fatal("unchanged change task accepted")
				}
			} else if err != nil || verdict != marshal.VerdictAccept {
				t.Fatalf("inspection refused: %s %v", verdict, err)
			}
		})
	}
}

func TestVerifierRefusalRetainsFindingsAndReason(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	startIntegrityRun(t, s, marshal.Budget{})
	run, err := s.Snapshot(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	s.IndependentVerify = func(_ context.Context, run marshal.Run, head string, session verification.Session) (marshal.VerifierEvidence, error) {
		return marshal.VerifierEvidence{Reviewer: "verifier:test", Provider: "test", Commit: head, Verdict: "fail", Findings: []string{"missing boundary test"}, InputDigest: verifierInputDigest(run, head, session)}, errors.New("independent verification refused")
	}
	if err := s.captureIndependentVerification(t.Context(), "run", run, run.BaseCommit, verification.Session{}); err == nil {
		t.Fatal("refused verifier accepted")
	}
	report, err := s.CompletionReport(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Verifiers) != 1 || report.Verifiers[0].Verdict != "fail" || len(report.Verifiers[0].Findings) != 1 || len(report.Risks) == 0 {
		t.Fatalf("refusal evidence lost: %+v", report)
	}
}

func TestRecoveredRequiredScanCannotSkipMissingGuard(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	s.HandInGuard = func(context.Context, string, string, marshal.HandIn) (string, error) {
		return "", errors.New("missing trap")
	}
	startIntegrityRun(t, s, marshal.Budget{})
	dispatch, err := s.Dispatch(t.Context(), "run", "a", "write")
	if err != nil {
		t.Fatal(err)
	}
	s.HandInGuard = nil
	if _, err := s.CollectHandIn(t.Context(), "run", dispatch); err != nil {
		t.Fatal(err)
	}
	run, err := s.Snapshot(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if run.Tasks[0].State == marshal.HandedIn || run.Tasks[0].State == marshal.Accepted {
		t.Fatal("missing required scan guard accepted after recovery")
	}
}
