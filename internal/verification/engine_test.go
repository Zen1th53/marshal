package verification

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

func fixture(now time.Time) Session {
	b := Binding{ProjectID: "p", GoalID: "g", GoalRevision: 1, PlanID: "plan", PlanVersion: 1, RunID: "run", RunVersion: 2, TreeDigest: "tree", EnvironmentDigest: "env"}
	return Session{ID: "verify", Version: 1, Binding: b, Criteria: []Criterion{{ID: "c", Mandatory: true, ClaimIDs: []string{"claim"}}}, Claims: []Claim{{ID: "claim", CriterionID: "c", Critical: true, SemanticScope: []string{"runtime"}, EvidenceIDs: []string{"e1", "e2"}}}, Evidence: []Evidence{
		{ID: "e1", ClaimID: "claim", Status: StatusPass, ContentDigest: "a", TreeDigest: "tree", EnvironmentDigest: "env", Producer: "alice", Provider: "p1", Oracle: "o1", ClusterID: "independent-a", Attempts: 1, Passes: 1},
		{ID: "e2", ClaimID: "claim", Status: StatusPass, ContentDigest: "b", TreeDigest: "tree", EnvironmentDigest: "env", Producer: "bob", Provider: "p2", Oracle: "o2", ClusterID: "independent-b", Attempts: 1, Passes: 1},
	}, RequiredChecks: map[string]Status{"security": StatusPass, "runtime_negative": StatusPass}, CreatedAt: now, UpdatedAt: now}
}

func TestCompletionFailsClosedAcrossProcess06Boundaries(t *testing.T) {
	now := time.Date(2026, 9, 8, 1, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*Session, *Binding)
		want   Decision
	}{
		{"goal revision", func(s *Session, b *Binding) { b.GoalRevision++ }, NeedsReplan},
		{"tree mutation", func(s *Session, b *Binding) { b.TreeDigest = "changed" }, NeedsReexecution},
		{"environment mutation", func(s *Session, b *Binding) { b.EnvironmentDigest = "changed" }, NeedsReexecution},
		{"critical contradiction", func(s *Session, b *Binding) {
			s.Contradictions = []Contradiction{{ID: "x", ClaimID: "claim", Critical: true}}
		}, VerificationFailed},
		{"missing evidence", func(s *Session, b *Binding) { s.Evidence = s.Evidence[:1] }, VerificationFailed},
		{"correlated evidence", func(s *Session, b *Binding) { s.Evidence[1].ClusterID = s.Evidence[0].ClusterID }, VerificationFailed},
		{"flaky evidence", func(s *Session, b *Binding) { s.Evidence[0].Attempts = 3; s.Evidence[0].Passes = 0 }, VerificationFailed},
		{"stale evidence", func(s *Session, b *Binding) { past := now.Add(-time.Second); s.Evidence[0].ExpiresAt = &past }, VerificationFailed},
		{"oracle not run", func(s *Session, b *Binding) { s.RequiredChecks["security"] = StatusNotRun }, Blocked},
		{"oracle unknown", func(s *Session, b *Binding) { s.RequiredChecks["security"] = StatusUnknown }, Blocked},
		{"critical waiver", func(s *Session, b *Binding) {
			s.Waivers = []Waiver{{ID: "w", Actor: "human", Reason: "ship", Critical: true, ExpiresAt: now.Add(time.Hour)}}
		}, Blocked},
		{"expired waiver", func(s *Session, b *Binding) {
			s.Waivers = []Waiver{{ID: "w", Actor: "human", Reason: "ship", ExpiresAt: now.Add(-time.Hour)}}
		}, Blocked},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := fixture(now)
			b := s.Binding
			tt.mutate(&s, &b)
			if got := Evaluate(s, b, now); got != tt.want {
				t.Fatalf("got %s want %s", got, tt.want)
			}
		})
	}
	base := fixture(now)
	if got := Evaluate(base, base.Binding, now); got != VerifiedComplete {
		t.Fatalf("valid session: %s", got)
	}
}

func TestAttestationTamperAndApplicability(t *testing.T) {
	now := time.Now().UTC()
	s := fixture(now)
	a, err := NewCompletionAttestation("a", s, "bundle", "marshal/process06", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Verify(s.Binding); err != nil {
		t.Fatal(err)
	}
	a.Decision = Blocked
	if err := a.Verify(s.Binding); err != ErrTampered {
		t.Fatalf("tamper got %v", err)
	}
	a, _ = NewCompletionAttestation("a", s, "bundle", "marshal/process06", now)
	b := s.Binding
	b.RunVersion++
	if err := a.Verify(b); err != ErrBindingMismatch {
		t.Fatalf("binding got %v", err)
	}
}

func TestReplayAndIntegrationFailClosed(t *testing.T) {
	out := []byte("deterministic output")
	h := sha256.Sum256(out)
	ev := Evidence{OutputDigest: hex.EncodeToString(h[:])}
	if err := Replay(ev, out); err != nil {
		t.Fatal(err)
	}
	if err := Replay(ev, []byte("mutated")); err != ErrReplayDivergence {
		t.Fatalf("got %v", err)
	}
	a := IntegrationAttestation{CandidateSHA: "candidate", MainSHA: "main", MergeTreeDigest: "tree", EvidenceBundleDigest: "bundle", CandidateIsAncestor: true, CriticalGates: map[string]Status{"tests": StatusPass}, IssuedAt: time.Now()}
	if _, err := NewIntegrationAttestation(a); err != nil {
		t.Fatal(err)
	}
	a.CriticalGates["race"] = StatusNotRun
	if _, err := NewIntegrationAttestation(a); err == nil {
		t.Fatal("NOT_RUN exact-main gate accepted")
	}
}

func TestSemanticScopeCannotBeEmpty(t *testing.T) {
	s := fixture(time.Now())
	s.Claims[0].SemanticScope = nil
	if err := ValidateSession(s); err == nil {
		t.Fatal("empty semantic scope accepted")
	}
}
