package verification

import (
	"strings"
	"testing"
	"time"
)

func TestCriterionProgressHonestEvidence(t *testing.T) {
	now := time.Now()
	base := fixture(now)
	for _, tc := range []struct {
		name   string
		change func(*Session, *Binding)
		status Status
		detail string
	}{
		{"verified", func(*Session, *Binding) {}, StatusPass, "verified criterion"},
		{"failed", func(s *Session, b *Binding) { s.Evidence[0].Status = StatusFail }, StatusFail, "canonical evidence status"},
		{"not run", func(s *Session, b *Binding) { s.Evidence[0].Status = StatusNotRun }, StatusNotRun, "canonical evidence status"},
		{"unknown", func(s *Session, b *Binding) { s.Evidence[0].Status = StatusUnknown }, StatusUnknown, "canonical evidence status"},
		{"missing reference", func(s *Session, b *Binding) { s.Evidence = s.Evidence[1:] }, StatusUnknown, "missing evidence reference"},
		{"missing digest", func(s *Session, b *Binding) { s.Evidence[0].ContentDigest = "" }, StatusUnknown, "missing evidence digest"},
		{"old goal", func(s *Session, b *Binding) { b.GoalRevision++ }, StatusUnknown, "stale goal revision"},
		{"changed tree", func(s *Session, b *Binding) { b.TreeDigest = "changed" }, StatusUnknown, "stale or unavailable binding"},
		{"expired", func(s *Session, b *Binding) { past := now.Add(-time.Second); s.Evidence[0].ExpiresAt = &past }, StatusUnknown, "stale evidence"},
		{"unavailable binding", func(s *Session, b *Binding) { *b = Binding{} }, StatusUnknown, "stale goal revision"},
		{"changed plan", func(s *Session, b *Binding) { b.PlanVersion++ }, StatusUnknown, "stale or unavailable binding"},
		{"changed run", func(s *Session, b *Binding) { b.RunVersion++ }, StatusUnknown, "stale or unavailable binding"},
		{"changed environment", func(s *Session, b *Binding) { b.EnvironmentDigest = "changed" }, StatusUnknown, "stale or unavailable binding"},
		{"no successful attempts", func(s *Session, b *Binding) { s.Evidence[0].Passes = 0 }, StatusUnknown, "verification requirements not satisfied"},
		{"correlated", func(s *Session, b *Binding) { s.Evidence[1].ClusterID = s.Evidence[0].ClusterID }, StatusUnknown, "verification requirements not satisfied"},
		{"required check not run", func(s *Session, b *Binding) { s.RequiredChecks["security"] = StatusNotRun }, StatusUnknown, "verification requirements not satisfied"},
		{"required check unknown", func(s *Session, b *Binding) { s.RequiredChecks["security"] = StatusUnknown }, StatusUnknown, "verification requirements not satisfied"},
		{"contradiction", func(s *Session, b *Binding) {
			s.Contradictions = []Contradiction{{ID: "x", ClaimID: "claim", Critical: true}}
		}, StatusUnknown, "verification requirements not satisfied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := fixture(now)
			b := s.Binding
			tc.change(&s, &b)
			rows := ProjectCriteria([]string{"c", "unmapped"}, s, b, now)
			if rows[0].Status != tc.status || !strings.Contains(rows[0].Detail, tc.detail) || len(rows[0].EvidenceIDs) != 2 {
				t.Fatalf("%+v", rows)
			}
			if rows[1].Status != StatusNotRun || rows[1].Detail != "missing evidence" {
				t.Fatalf("%+v", rows[1])
			}
		})
	}
	if len(ProjectCriteria(nil, base, base.Binding, now)) != 0 {
		t.Fatal("invented criteria")
	}
}
