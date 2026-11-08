//go:build !governed

package plan

import (
	"encoding/json"
	"github.com/Zen1th53/marshal/internal/model"
	"strings"
	"testing"
	"time"
)

func TestProducerApprovalSealDetachedAndBoundToReviewedVersion(t *testing.T) {
	now := time.Now().UTC()
	expiry := now.Add(time.Hour)
	p := ExecutionPlan{Version: 4, State: StateReady, Tasks: []Task{{ID: "work", Instructions: "retained", Paths: []string{"marker"}}}, Assignments: AssignmentPlan{Assignments: []Assignment{{Role: RoleDeveloper, Tasks: []string{"work"}, Harness: "codex", Provider: "codex", Model: "primary", Fallback: &Fallback{Harness: "agy", Provider: "agy", Model: "fallback", ProbedAt: &now, ExpiresAt: &expiry}}}}}
	approved, err := p.Approve(now)
	if err != nil {
		t.Fatal(err)
	}
	seal := approved.ApprovedScope.ProducerAssignments
	if seal == nil || seal.ReviewedPlanVersion != 4 || len(seal.Assignments) != 1 {
		t.Fatal("missing historical review")
	}
	wantExpiry := expiry
	digest := approved.ApprovalScopeDigest
	p.Assignments.Assignments[0].Tasks[0] = "foreign"
	p.Assignments.Assignments[0].Fallback.Model = "changed"
	*p.Assignments.Assignments[0].Fallback.ExpiresAt = now.Add(-time.Hour)
	if seal.Assignments[0].Tasks[0] != "work" || seal.Assignments[0].Fallback.Model != "fallback" || !seal.Assignments[0].Fallback.ExpiresAt.Equal(wantExpiry) || approved.ApprovedScope.Digest() != digest {
		t.Fatal("producer snapshot aliases mutable plan")
	}
	seal.Assignments[0].Fallback.Model = "tampered"
	if approved.ApprovedScope.Digest() == digest {
		t.Fatal("selector omitted from owner digest")
	}
	revised, err := approved.Revise(approved.Version, "fresh owner decision", now)
	if err != nil {
		t.Fatal(err)
	}
	revised.State = StateReady
	refreshed, err := revised.Approve(now)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.ApprovedScope.ProducerAssignments.ReviewedPlanVersion != 5 || refreshed.ApprovalScopeDigest == digest {
		t.Fatal("reapproval retained stale seal")
	}
}
func TestProducerApprovalSealOnlyDeveloperAndBounded(t *testing.T) {
	p := ExecutionPlan{State: StateReady, Version: 1, Assignments: AssignmentPlan{Assignments: []Assignment{{Role: RoleQA}, {Role: RoleDeveloper, Independent: true}}}}
	a, err := p.Approve(time.Now())
	if err != nil || a.ApprovedScope.ProducerAssignments != nil {
		t.Fatal("independent role became producer", err)
	}
	p.Assignments.Assignments = append(p.Assignments.Assignments, Assignment{Role: RoleDeveloper, Reason: strings.Repeat("x", 64<<10)})
	if a, err = p.Approve(time.Now()); err == nil || a.State != StateReady {
		t.Fatal("oversized owner snapshot accepted")
	}
	p.Version = 0
	a, err = p.Approve(time.Now())
	if err != nil || a.ApprovedScope.ProducerAssignments != nil {
		t.Fatal("legacy plan acquired guessed historical version")
	}
}
func TestProducerFallbackPinsAreDetachedAndOptional(t *testing.T) {
	now := time.Now().UTC()
	expiry := now.Add(time.Hour)
	c := HarnessCandidate{InstalledVersion: "v1", Profile: model.HarnessProfile{ProbeEvidenceID: "probe", ProbedAt: now, ExpiresAt: expiry}}
	var f Fallback
	pinPrivateFallbackProfile(c, &f)
	if f.HarnessVersion != "v1" || f.ProbeEvidenceID != "probe" || f.ProbedAt == nil || !f.ProbedAt.Equal(now) || f.ExpiresAt == nil || !f.ExpiresAt.Equal(expiry) {
		t.Fatal("missing profile witness")
	}
	c.Profile.ProbedAt = expiry
	if !f.ProbedAt.Equal(now) {
		t.Fatal("timestamp aliases profile")
	}
	var legacy Fallback
	pinPrivateFallbackProfile(HarnessCandidate{}, &legacy)
	raw, _ := json.Marshal(legacy)
	if strings.Contains(string(raw), "probed_at") || strings.Contains(string(raw), "expires_at") {
		t.Fatal("missing probe invented")
	}
}
