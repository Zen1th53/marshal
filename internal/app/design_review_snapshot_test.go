package app

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestBindApprovedPlanRequiresDesignReceipt(t *testing.T) {
	s, _ := marshalBoundBudgetFixture(t, marshal.Budget{})
	ctx := t.Context()
	settings, err := s.Store.GetMarshalSettings(ctx, s.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	settings.Value.ReviewPolicy.Design = marshal.DesignReviewRequired
	if _, err := s.Store.SetMarshalSettings(ctx, s.ProjectID, settings.Value, settings.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BindApprovedPlan(ctx, "review-required"); err == nil || !strings.Contains(err.Error(), "design review receipt") {
		t.Fatalf("binding without required receipt: %v", err)
	}
	if _, err := s.Store.GetMarshalRun(ctx, s.ProjectID, "review-required"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("refused binding persisted a run: %v", err)
	}
}

func TestDesignReviewPayloadDigestUsesReviewedSnapshot(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	ctx := t.Context()
	if _, err := s.StartPlanning(ctx, "review-snapshot", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.Snapshot(ctx, "review-snapshot")
	if err != nil {
		t.Fatal(err)
	}
	current, revision, err := s.readRun(ctx, "review-snapshot")
	if err != nil {
		t.Fatal(err)
	}
	current.Tasks[0].Instructions = "changed after the reviewer snapshot"
	if err := s.save(ctx, "review-snapshot", current, revision); err != nil {
		t.Fatal(err)
	}
	payload, digest := formatRunDesignDraftForReview(snapshot)
	var reviewed struct {
		DraftDigest string `json:"draft_digest"`
		Tasks       []struct {
			Instructions string `json:"instructions"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(payload), &reviewed); err != nil {
		t.Fatal(err)
	}
	if digest != runDesignDraftDigest(snapshot) || reviewed.DraftDigest != digest || len(reviewed.Tasks) != 1 || reviewed.Tasks[0].Instructions != snapshot.Tasks[0].Instructions {
		t.Fatalf("review payload and digest differ from snapshot: %s", payload)
	}
	if digest == runDesignDraftDigest(current) {
		t.Fatal("review of old snapshot certifies amended draft")
	}
	receipt := marshal.DesignReviewReceipt{Provider: "claude", SessionID: "fresh-review", FreshSession: true, DraftDigest: digest, Findings: "reviewed snapshot", ReviewedAt: time.Now().UTC()}
	if _, err := s.RecordDesignReview(ctx, "review-snapshot", receipt); err == nil {
		t.Fatal("receipt for reviewed snapshot attached to amended draft")
	}
}
