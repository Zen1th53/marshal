package app

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/verification"
)

func TestGoalProgressCanonicalEvidenceAndRevisionStaleness(t *testing.T) {
	r, ctx, g, run := requiredGoalReportFixture(t)
	binding, err := r.Verification().BindingForRun(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	s := verification.Session{ID: "report-verification", Version: 1}
	statuses := []verification.Status{verification.StatusPass, verification.StatusFail, verification.StatusNotRun, verification.StatusUnknown, verification.StatusPass, verification.StatusPass}
	for i, status := range statuses {
		name := g.SuccessCriteria[i]
		id := "evidence-" + name
		s.Criteria = append(s.Criteria, verification.Criterion{ID: name, Mandatory: true, ClaimIDs: []string{name}})
		s.Claims = append(s.Claims, verification.Claim{ID: name, CriterionID: name, SemanticScope: []string{"README.md"}, EvidenceIDs: []string{id}})
		if name == "missing" {
			continue
		}
		ev := verification.Evidence{ID: id, ClaimID: name, Status: status, ContentDigest: "digest", TreeDigest: binding.TreeDigest, EnvironmentDigest: binding.EnvironmentDigest, Attempts: 1, Passes: 1}
		if name == "expired" {
			past := time.Now().Add(-time.Hour)
			ev.ExpiresAt = &past
		}
		s.Evidence = append(s.Evidence, ev)
	}
	s, err = r.Verification().StartForRun(ctx, run.RunID, s)
	if err != nil {
		t.Fatal(err)
	}
	progress, err := r.GoalProgress(ctx, g.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	want := []verification.Status{verification.StatusPass, verification.StatusFail, verification.StatusNotRun, verification.StatusUnknown, verification.StatusUnknown, verification.StatusUnknown, verification.StatusNotRun}
	if len(progress.Criteria) != len(want) {
		t.Fatalf("criteria: %+v", progress)
	}
	for i, row := range progress.Criteria {
		if row.Status != want[i] || row.Criterion != g.SuccessCriteria[i] || (i < len(statuses) && len(row.EvidenceIDs) != 1) {
			t.Fatalf("criterion %d: %+v", i, row)
		}
	}
	stored, err := r.Verification().Current(ctx, s.ID)
	if err != nil || !reflect.DeepEqual(stored, s) {
		t.Fatalf("report mutated verification: %+v %v", stored, err)
	}
	// Keep the same criterion strings across revisions: evidence must still expire.
	g.Revision++
	if err := r.Store().SaveGoalContract(ctx, g, g.Revision-1); err != nil {
		t.Fatal(err)
	}
	progress, err = r.GoalProgress(ctx, g.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range progress.Criteria[:len(statuses)] {
		if row.Status != verification.StatusUnknown || !strings.Contains(row.Detail, "stale goal revision") || len(row.EvidenceIDs) != 1 {
			t.Fatalf("old evidence promoted or hidden: %+v", row)
		}
	}
}
