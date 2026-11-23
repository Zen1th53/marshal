package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
)

func TestDesignReviewReceiptBoundToDraft(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	r, err := Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })

	bin := t.TempDir()
	out := filepath.Join(t.TempDir(), "args")
	t.Setenv("MARSHAL_TEST_REVIEW_ARGS", out)
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	script := `#!/bin/sh
printf '%s\n' "$@" > "$MARSHAL_TEST_REVIEW_ARGS"
printf '%s\n' '{"type":"result","session_id":"fresh-design-session","structured_output":{"findings":"Confirm error handling around retry state."}}'
`
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))

	draftText := "Draft v1: split verification and review policy snapshot"
	req := DesignReviewRequest{Provider: "claude", Draft: draftText, ReadingList: []string{"README.md"}}
	result, err := r.ReviewDesign(ctx, "codex", req)
	if err != nil {
		t.Fatalf("ReviewDesign: %v", err)
	}
	if !result.FreshSession || result.SessionID != "fresh-design-session" {
		t.Fatalf("expected fresh session receipt, got %+v", result)
	}
	wantDigest := marshal.DesignDraftDigest(draftText)
	if result.DraftDigest != wantDigest {
		t.Fatalf("draft digest = %q, want %q", result.DraftDigest, wantDigest)
	}
	receipt := result.Receipt()
	if err := receipt.ValidateForDraft(wantDigest); err != nil {
		t.Fatalf("valid receipt refused: %v", err)
	}
	if err := receipt.ValidateForDraft(marshal.DesignDraftDigest(draftText + " mutated")); err == nil {
		t.Fatal("stale draft digest accepted")
	}
	nonFresh := receipt
	nonFresh.FreshSession = false
	if err := nonFresh.ValidateForDraft(wantDigest); err == nil {
		t.Fatal("non-fresh design review receipt accepted")
	}

	// Verify run binding through the real Runtime.ReviewDesign path when
	// Design=required, refusal of a caller-text-only or mismatched receipt, and
	// invalidation when any draft detail changes (even with Pack.Requirements unchanged).
	settings, err := r.Store().GetMarshalSettings(ctx, r.ProjectID())
	if err != nil {
		t.Fatal(err)
	}
	settings.Value.ReviewPolicy.Design = marshal.DesignReviewRequired
	if _, err := r.Store().SetMarshalSettings(ctx, r.ProjectID(), settings.Value, settings.Revision); err != nil {
		t.Fatal(err)
	}
	svc := r.Marshal()
	svc.ApprovalActor = func(context.Context, string, string) (string, error) { return "user", nil }
	pt := plan.Task{
		ID:           "a",
		Title:        "write a",
		Criteria:     []string{"a.txt exists"},
		Paths:        []string{"a.txt"},
		Instructions: "write initial a.txt",
		Mutating:     true,
		Weight:       1,
	}
	graph, err := plan.BuildGraph([]plan.Task{pt})
	if err != nil {
		t.Fatal(err)
	}
	runDraft := MarshalDraft{
		Plan: plan.ExecutionPlan{
			ID:        "PLAN-design",
			ProjectID: projectid.ID(r.ProjectIdentity()),
			Goal:      plan.GoalBinding{GoalID: "GOAL-design", Revision: 1},
			Version:   1,
			State:     plan.StateReady,
			Mode:      plan.ModeStandard,
			Tasks:     []plan.Task{pt},
			Checks:    map[string][]string{"a": {"test -f a.txt"}},
			Graph:     graph,
		},
		Tasks: []marshal.Task{{
			PlanTaskID:   "a",
			Title:        "write a",
			Worker:       "worker",
			Mode:         marshal.Native,
			Files:        []string{"a.txt"},
			Criteria:     []string{"a.txt exists"},
			Checks:       []marshal.Check{{Command: "test -f a.txt", Criteria: []string{"a.txt exists"}}},
			Instructions: "write initial a.txt",
		}},
	}
	pack := marshal.PlanPack{
		Requirements: draftText,
		Index:        "a: write a.txt",
		Tasks:        map[string]string{"a": "write a.txt"},
	}
	pack.Digest = planPackDigest(pack)
	runDraft.Pack = &pack
	if _, err := svc.StartPlanningFromDraft(ctx, "run-real-design", "write", runDraft, marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Approve(ctx, "run-real-design"); err == nil || !strings.Contains(err.Error(), "design review receipt") {
		t.Fatalf("approval without required design review receipt: %v", err)
	}
	// A receipt computed only over Pack.Requirements (not the full draft) must be refused.
	if _, err := svc.RecordDesignReview(ctx, "run-real-design", receipt); err == nil {
		t.Fatal("requirements-only receipt was attached to full draft run")
	}
	fullDigest, err := svc.RunDesignDraftDigest(ctx, "run-real-design")
	if err != nil {
		t.Fatal(err)
	}
	if fullDigest == wantDigest {
		t.Fatalf("full draft digest must not collapse to Pack.Requirements alone: %s", fullDigest)
	}
	// Real design review path attaches the receipt bound to RunDesignDraftDigest.
	boundResult, err := r.ReviewDesign(ctx, "codex", DesignReviewRequest{
		Provider:    "claude",
		Draft:       "Short summary for reviewer",
		ReadingList: []string{"README.md"},
	})
	if err != nil {
		t.Fatalf("Runtime.ReviewDesign on drafting run: %v", err)
	}
	if boundResult.RunID != "run-real-design" || boundResult.DraftDigest != fullDigest {
		t.Fatalf("unexpected bound design review result: %+v (want run=%s digest=%s)", boundResult, "run-real-design", fullDigest)
	}
	reviewedArgs, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(reviewedArgs), "write initial a.txt") || !strings.Contains(string(reviewedArgs), fullDigest) {
		t.Fatalf("reviewer did not receive full canonical run draft: %s", reviewedArgs)
	}
	approvedReal, err := svc.Approve(ctx, "run-real-design")
	if err != nil {
		t.Fatalf("Approve after real ReviewDesign path: %v", err)
	}
	if approvedReal.DesignReview == nil || approvedReal.DesignReview.DraftDigest != fullDigest {
		t.Fatalf("approved run missing bound design review receipt: %+v", approvedReal.DesignReview)
	}

	// Verify scoped amendment changing task instructions (keeping Pack.Requirements
	// identical) changes RunDesignDraftDigest and clears DesignReview, while major
	// amendment preserves historical review verdicts.
	s, _ := marshalFixture(t, 1)
	sSettings, err := s.Store.GetMarshalSettings(ctx, s.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	sSettings.Value.ReviewPolicy.Design = marshal.DesignReviewRequired
	if _, err := s.Store.SetMarshalSettings(ctx, s.ProjectID, sSettings.Value, sSettings.Revision); err != nil {
		t.Fatal(err)
	}
	draft, err := s.Model.Draft(ctx, "write")
	if err != nil {
		t.Fatal(err)
	}
	draft.Pack = &pack
	if _, err := s.StartPlanningFromDraft(ctx, "run-design", "write", draft, marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	runDesignDigest, err := s.RunDesignDraftDigest(ctx, "run-design")
	if err != nil {
		t.Fatal(err)
	}
	matchingReceipt := receipt
	matchingReceipt.DraftDigest = runDesignDigest
	if _, err := s.RecordDesignReview(ctx, "run-design", matchingReceipt); err != nil {
		t.Fatalf("RecordDesignReview: %v", err)
	}
	approved, err := s.Approve(ctx, "run-design")
	if err != nil {
		t.Fatalf("Approve with matching design receipt: %v", err)
	}
	if approved.ReviewPolicy == nil || approved.ReviewPolicy.PolicyVersion != marshal.ReviewPolicyVersion ||
		approved.ReviewPolicy.Design != marshal.DesignReviewRequired || approved.ReviewPolicy.Actor == "" || approved.ReviewPolicy.ConfirmedAt.IsZero() {
		t.Fatalf("unexpected review policy snapshot: %+v", approved.ReviewPolicy)
	}
	if err := s.AuthorizeDelivery(ctx, "run-design", approved.ApprovalScopeDigest); err != nil {
		t.Fatalf("AuthorizeDelivery: %v", err)
	}

	// Scoped amendment changing task title while keeping Pack.Requirements identical
	// must stay Approved, change RunDesignDraftDigest, and clear the stale DesignReview receipt.
	approvedPlan, err := s.Store.GetPlan(ctx, approved.PlanID, approved.PlanVersion)
	if err != nil {
		t.Fatal(err)
	}
	scopedDraft := draft
	scopedDraft.Plan = approvedPlan
	scopedDraft.Plan.State = plan.StateReady
	scopedDraft.Plan.Routes = map[string]plan.Route{"a": {Provider: "test", Governance: constitution.GovernanceVerified}}
	scopedDraft.Plan.Tasks = append([]plan.Task(nil), approvedPlan.Tasks...)
	scopedDraft.Plan.Tasks[0].Title = "write a (refined scoped title)"
	scopedGraph, err := plan.BuildGraph(scopedDraft.Plan.Tasks)
	if err != nil {
		t.Fatal(err)
	}
	scopedDraft.Plan.Graph = scopedGraph
	scopedDraft.Tasks = append([]marshal.Task(nil), draft.Tasks...)
	scopedDraft.Tasks[0].Title = "write a (refined scoped title)"
	scopedAmended, err := s.ApplyAmendDraft(ctx, "run-design", "clarify task title within scope", scopedDraft)
	if err != nil {
		t.Fatalf("scoped ApplyAmendDraft: %v", err)
	}
	if scopedAmended.State != marshal.Approved {
		t.Fatalf("expected scoped amendment to remain Approved, got %s", scopedAmended.State)
	}
	if scopedAmended.DesignReview != nil {
		t.Fatalf("stale design review receipt survived scoped task change: %+v", scopedAmended.DesignReview)
	}
	afterScopedDigest, err := s.RunDesignDraftDigest(ctx, "run-design")
	if err != nil {
		t.Fatal(err)
	}
	if afterScopedDigest == runDesignDigest {
		t.Fatalf("RunDesignDraftDigest did not change when task details changed with Pack.Requirements unchanged")
	}

	// Execute and review once so a historical verdict exists before major amendment.
	d, err := s.Dispatch(ctx, "run-design", "a", "write")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CollectHandIn(ctx, "run-design", d); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Review(ctx, "run-design", "a", knownCharge()); err != nil {
		t.Fatal(err)
	}
	beforeReview, err := s.Store.GetMarshalReview(ctx, "run-design", "a", 1)
	if err != nil {
		t.Fatal(err)
	}

	// Major amendment changing task instructions and checks (while keeping Pack.Requirements
	// identical) transitions back to Drafting, clears DesignReview, voids close authorization,
	// refuses the prior receipt, and preserves the prior review verdict historically.
	amendDraft := scopedDraft
	amendDraft.Plan.Tasks = append([]plan.Task(nil), scopedDraft.Plan.Tasks...)
	amendDraft.Plan.Tasks[0].Instructions = "updated task instructions"
	amendDraft.Plan.Checks = map[string][]string{"a": {"test -f a.txt && true"}}
	amendGraph, err := plan.BuildGraph(amendDraft.Plan.Tasks)
	if err != nil {
		t.Fatal(err)
	}
	amendDraft.Plan.Graph = amendGraph
	amendDraft.Tasks = append([]marshal.Task(nil), scopedDraft.Tasks...)
	amendDraft.Tasks[0].Instructions = "updated task instructions"
	amendDraft.Tasks[0].Checks = []marshal.Check{{Command: "test -f a.txt && true", Criteria: scopedDraft.Tasks[0].Criteria}}
	amended, err := s.ApplyAmendDraft(ctx, "run-design", "change instructions and checks", amendDraft)
	if err != nil {
		t.Fatalf("ApplyAmendDraft: %v", err)
	}
	if amended.DesignReview != nil {
		t.Fatalf("design review receipt survived material amendment: %+v", amended.DesignReview)
	}
	if amended.CloseAuthorization != nil && !amended.CloseAuthorization.Voided {
		t.Fatalf("close authorization not voided after amendment: %+v", amended.CloseAuthorization)
	}
	// Re-attaching the prior draft's receipt to the amended drafting run must be refused
	// even though Pack.Requirements did not change.
	if _, err := s.RecordDesignReview(ctx, "run-design", matchingReceipt); err == nil {
		t.Fatal("stale receipt from prior draft was accepted on amended drafting run")
	}
	afterReview, err := s.Store.GetMarshalReview(ctx, "run-design", "a", 1)
	if err != nil || afterReview.Value.Verdict != beforeReview.Value.Verdict {
		t.Fatalf("historical verdict lost: before=%+v after=%+v err=%v", beforeReview.Value, afterReview.Value, err)
	}
}
