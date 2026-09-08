package verification

import (
	"testing"
	"time"
)

func FuzzCompletionNeverPassesUnboundEvidence(f *testing.F) {
	f.Add("tree", "env", "other", true)
	f.Add("tree", "env", "tree", false)
	f.Fuzz(func(t *testing.T, tree, environment, evidenceTree string, secondCluster bool) {
		if tree == "" || environment == "" {
			return
		}
		s := fixture(time.Unix(1, 0))
		s.Binding.TreeDigest = tree
		s.Binding.EnvironmentDigest = environment
		s.Evidence[0].TreeDigest = evidenceTree
		s.Evidence[1].TreeDigest = evidenceTree
		if !secondCluster {
			s.Evidence[1].ClusterID = s.Evidence[0].ClusterID
		}
		got := Evaluate(s, s.Binding, time.Unix(2, 0))
		if got == VerifiedComplete && (evidenceTree != tree || !secondCluster) {
			t.Fatalf("unbound/correlated evidence reached completion")
		}
	})
}

func BenchmarkCompletionDecision1000Evidence(b *testing.B) {
	now := time.Unix(1, 0)
	s := fixture(now)
	s.Claims[0].Critical = false
	s.Claims[0].EvidenceIDs = nil
	s.Evidence = nil
	for i := 0; i < 1000; i++ {
		id := string(rune(i + 1))
		s.Claims[0].EvidenceIDs = append(s.Claims[0].EvidenceIDs, id)
		s.Evidence = append(s.Evidence, Evidence{ID: id, ClaimID: "claim", Status: StatusPass, ContentDigest: "digest", TreeDigest: "tree", EnvironmentDigest: "env", Attempts: 1, Passes: 1})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if Evaluate(s, s.Binding, now) != VerifiedComplete {
			b.Fatal("unexpected decision")
		}
	}
}
