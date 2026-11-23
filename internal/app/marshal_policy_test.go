package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/plan"
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

	// Verify run binding, approval requirement when Design=required, and
	// invalidation when an amendment changes requirements or checks.
	s, _ := marshalFixture(t, 1)
	settings, err := s.Store.GetMarshalSettings(ctx, s.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	settings.Value.ReviewPolicy.Design = marshal.DesignReviewRequired
	if _, err := s.Store.SetMarshalSettings(ctx, s.ProjectID, settings.Value, settings.Revision); err != nil {
		t.Fatal(err)
	}
	draft, err := s.Model.Draft(ctx, "write")
	if err != nil {
		t.Fatal(err)
	}
	pack := marshal.PlanPack{
		Requirements: draftText,
		Index:        "a: write a.txt",
		Tasks:        map[string]string{"a": "write a.txt"},
	}
	pack.Digest = planPackDigest(pack)
	draft.Pack = &pack
	if _, err := s.StartPlanningFromDraft(ctx, "run-design", "write", draft, marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run-design"); err == nil || !strings.Contains(err.Error(), "design review receipt") {
		t.Fatalf("approval without required design review receipt: %v", err)
	}
	wrongReceipt := receipt
	wrongReceipt.DraftDigest = marshal.DesignDraftDigest("other draft")
	if _, err := s.RecordDesignReview(ctx, "run-design", wrongReceipt); err == nil {
		t.Fatal("mismatched draft receipt was attached")
	}
	if _, err := s.RecordDesignReview(ctx, "run-design", receipt); err != nil {
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

	// Execute and review once so a historical verdict exists before amendment.
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

	// Amend checks and requirements: invalidates design receipt and close
	// authorization while preserving the prior review verdict historically.
	amendDraft := draft
	amendDraft.Plan.Tasks = append([]plan.Task(nil), draft.Plan.Tasks...)
	amendDraft.Plan.Checks = map[string][]string{"a": {"test -f a.txt && true"}}
	amendDraft.Tasks = append([]marshal.Task(nil), draft.Tasks...)
	amendDraft.Tasks[0].Checks = []marshal.Check{{Command: "test -f a.txt && true", Criteria: draft.Tasks[0].Criteria}}
	amendPack := marshal.PlanPack{
		Requirements: draftText + "\nupdated requirements",
		Index:        "a: write a.txt",
		Tasks:        map[string]string{"a": "write a.txt"},
	}
	amendPack.Digest = planPackDigest(amendPack)
	amendDraft.Pack = &amendPack
	amended, err := s.ApplyAmendDraft(ctx, "run-design", "change checks and requirements", amendDraft)
	if err != nil {
		t.Fatalf("ApplyAmendDraft: %v", err)
	}
	if amended.DesignReview != nil {
		t.Fatalf("design review receipt survived material amendment: %+v", amended.DesignReview)
	}
	if amended.CloseAuthorization != nil && !amended.CloseAuthorization.Voided {
		t.Fatalf("close authorization not voided after amendment: %+v", amended.CloseAuthorization)
	}
	afterReview, err := s.Store.GetMarshalReview(ctx, "run-design", "a", 1)
	if err != nil || afterReview.Value.Verdict != beforeReview.Value.Verdict {
		t.Fatalf("historical verdict lost: before=%+v after=%+v err=%v", beforeReview.Value, afterReview.Value, err)
	}
}
