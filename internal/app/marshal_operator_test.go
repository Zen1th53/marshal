package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
)

func TestMarshalChargePausesForUnmeasurableCeiling(t *testing.T) {
	for _, budget := range []marshal.Budget{{Tokens: marshal.Ceiling{Task: 10}}, {Money: marshal.Ceiling{Plan: 10}}} {
		s, _ := marshalFixture(t, 1)
		if _, err := s.StartPlanningFromDraft(t.Context(), "run", "write", s.Model.(marshalFakeModel).draft, budget); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Approve(t.Context(), "run"); err != nil {
			t.Fatal(err)
		}
		result, err := s.Charge(t.Context(), "run", "a", "worker", unknownMarshalCharge())
		if err != nil {
			t.Fatal(err)
		}
		run, err := s.Snapshot(t.Context(), "run")
		if err != nil || run.State != marshal.AwaitingUser || run.Tasks[0].State != marshal.Queued || len(result.TaskExceeded)+len(result.PlanExceeded) != 0 {
			t.Fatalf("unknown charge: %+v %+v %v", result, run, err)
		}
	}
}

func TestMarshalDispatchChecksStoredCeiling(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	if _, err := s.StartPlanningFromDraft(t.Context(), "run", "write", s.Model.(marshalFakeModel).draft, marshal.Budget{WallTime: marshal.Ceiling{Plan: 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(t.Context(), "run"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Charge(t.Context(), "run", "", "planning", marshal.Charge{Tokens: marshal.Amount{Known: true}, Money: marshal.Amount{Known: true}, WallTime: time.Second}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Dispatch(t.Context(), "run", "a", "write"); err == nil {
		t.Fatal("launched with no wall time remaining")
	}
	run, err := s.Snapshot(t.Context(), "run")
	if err != nil || run.State != marshal.AwaitingUser || run.Tasks[0].State != marshal.Queued {
		t.Fatalf("run: %+v %v", run, err)
	}
}

func TestMarshalWorkerStopsAtWallCeiling(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(ctx context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	if _, err := s.StartPlanningFromDraft(t.Context(), "run", "write", s.Model.(marshalFakeModel).draft, marshal.Budget{WallTime: marshal.Ceiling{Task: 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(t.Context(), "run"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	start := time.Now()
	d, err := s.Dispatch(ctx, "run", "a", "write")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.CollectHandIn(ctx, "run", d)
	if time.Since(start) >= 3*time.Second {
		t.Fatal("worker ran past its ceiling")
	}
	run, err := s.Snapshot(t.Context(), "run")
	if err != nil || run.Tasks[0].State != marshal.Returned {
		t.Fatalf("timed out task: %+v %v", run, err)
	}
}

func TestMarshalDraftWithSingleProvider(t *testing.T) {
	root := t.TempDir()
	proposal := `{"tasks":[{"id":"a","title":"write a","criteria":["a exists"],"paths":["a.txt"],"depends_on":[],"worker":"claude","checks":[{"command":"test -f a.txt","criteria":["a exists"]}]}]}`
	stub := "#!/bin/sh\nprintf '%s\\n' '" + `{"structured_output":` + proposal + `,"session_id":"planner"}` + "'\n"
	if err := os.WriteFile(filepath.Join(root, "claude"), []byte(stub), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	cli := &MarshalCLI{Provider: "claude", Dir: root, ProjectID: "PROJECT-0123456789abcdef0123456789abcdef"}
	draft, err := cli.Draft(t.Context(), "write a")
	if err != nil || len(draft.Tasks) != 1 || draft.Tasks[0].Worker != "claude" {
		t.Fatalf("draft: %+v %v", draft, err)
	}
}

func TestMarshalCompletionReportUsesStoredEvidence(t *testing.T) {
	s, _ := marshalFixture(t, 2)
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
	if _, err := s.CollectHandIn(t.Context(), "run", d); err != nil {
		t.Fatal(err)
	}
	report, err := s.CompletionReport(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Criteria) != 2 || report.Criteria[0].Status != "verified" || report.Criteria[1].Status != "not tested" {
		t.Fatalf("report: %+v", report)
	}
	if len(report.Untested) != 1 || !strings.Contains(report.Untested[0], "b") {
		t.Fatalf("untested: %+v", report.Untested)
	}
	if report.Usage.Tokens.Known {
		t.Fatal("unmeasured dispatch usage was reported as known")
	}
}

func TestMarshalHelpCloseRecoveryInstruction(t *testing.T) {
	s, repo := marshalFixture(t, 1)
	if _, err := s.StartPlanning(t.Context(), "run", "write", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(t.Context(), "run"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Execute(t.Context(), "run", func(marshal.Task, BriefContext) string { return "write" }, nil); err != nil {
		t.Fatal(err)
	}
	// A clean main checkout now closes directly; recovery still applies to
	// an operator's dirty checkout, with the original message assertions.
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("operator edit"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(t.Context(), "run"); err == nil || !strings.Contains(err.Error(), "git switch --detach") || strings.Contains(err.Error(), "fast-forward it") {
		t.Fatalf("close recovery: %v (repo %s)", err, repo)
	}
}

func TestCompletionReportLabelsStandardReview(t *testing.T) {
	ctx := context.Background()
	s, _ := marshalFixture(t, 1)
	if _, err := s.StartPlanning(ctx, "run-standard-report", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run-standard-report"); err != nil {
		t.Fatal(err)
	}
	d, err := s.Dispatch(ctx, "run-standard-report", "a", "write files")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CollectHandIn(ctx, "run-standard-report", d); err != nil {
		t.Fatal(err)
	}
	report, err := s.CompletionReport(ctx, "run-standard-report")
	if err != nil {
		t.Fatal(err)
	}
	if report.ReviewLabel != "Standard: review by the Marshal itself; independent review in ULTRA" {
		t.Fatalf("unexpected review label: %q", report.ReviewLabel)
	}
	if report.ChecksLabel != "approved checks passed" {
		t.Fatalf("unexpected checks label: %q", report.ChecksLabel)
	}
}

func TestCompletionReportChecksLabelAggregate(t *testing.T) {
	ctx := context.Background()

	// 1. Untested when just started planning
	s1, _ := marshalFixture(t, 1)
	if _, err := s1.StartPlanning(ctx, "run-untested", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	rep1, err := s1.CompletionReport(ctx, "run-untested")
	if err != nil {
		t.Fatal(err)
	}
	if rep1.ChecksLabel != "not tested" {
		t.Fatalf("expected 'not tested', got %q", rep1.ChecksLabel)
	}

	// 2. Incomplete when 1 of 2 tasks passed and other not tested
	s2, _ := marshalFixture(t, 2)
	if _, err := s2.StartPlanning(ctx, "run-incomplete", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Approve(ctx, "run-incomplete"); err != nil {
		t.Fatal(err)
	}
	d2, err := s2.Dispatch(ctx, "run-incomplete", "a", "write files")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s2.CollectHandIn(ctx, "run-incomplete", d2); err != nil {
		t.Fatal(err)
	}
	rep2, err := s2.CompletionReport(ctx, "run-incomplete")
	if err != nil {
		t.Fatal(err)
	}
	if rep2.ChecksLabel != "incomplete" {
		t.Fatalf("expected 'incomplete', got %q", rep2.ChecksLabel)
	}

	// 3. Failed when a check fails
	s3, _ := marshalFixture(t, 1)
	if _, err := s3.StartPlanning(ctx, "run-failed", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	run3, _, err := s3.load(ctx, "run-failed")
	if err != nil {
		t.Fatal(err)
	}
	c3 := run3.Tasks[0].Checks[0]
	if _, err := s3.Store.SetMarshalHandIn(ctx, "run-failed", "a", 1, marshal.HandIn{
		ResultCommit: "res",
		CheckResults: []marshal.CheckResult{{Command: c3.Command, Criteria: c3.Criteria, ResultCommit: "res", Passed: false}},
	}); err != nil {
		t.Fatal(err)
	}
	rep3, err := s3.CompletionReport(ctx, "run-failed")
	if err != nil {
		t.Fatal(err)
	}
	if rep3.ChecksLabel != "failed" {
		t.Fatalf("expected 'failed', got %q", rep3.ChecksLabel)
	}
}
