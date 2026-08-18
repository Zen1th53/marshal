package adapter

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/gate"
	"github.com/Zen1th53/marshal/internal/verify/quorum"
)

type quorumCheckSource struct {
	requirements []quorum.Requirement
	attestations []quorum.Attestation
	provenance   quorum.Provenance
}

func (s quorumCheckSource) Resolve(context.Context, gate.CheckRequest) ([]quorum.Requirement, []quorum.Attestation, quorum.Provenance, error) {
	return s.requirements, s.attestations, s.provenance, nil
}

func TestQuorumCheckMapsSatisfiedEvaluationToGatePass(t *testing.T) {
	created := time.Unix(100, 0).UTC()
	check := NewQuorumCheck(quorum.NewEngine(func() time.Time { return created }), quorumCheckSource{
		requirements: []quorum.Requirement{{Kind: "security", Minimum: 1, Independent: true, AllowedRoles: []string{"reviewer"}}},
		attestations: []quorum.Attestation{{Subject: "agent-a", Provider: "Codex", Role: "reviewer", ChangeID: "change-1", EvidenceID: "evidence-1", Kind: "security", Result: quorum.ResultPass, ContentDigest: "sha256:change", CreatedAt: created}},
		provenance:   quorum.Provenance{ChangeID: "change-1", ContentDigest: "sha256:change"},
	})
	result, err := check(context.Background(), gate.CheckRequest{Point: gate.GatePointPreMerge, Subject: "agent-a", Resource: "change-1"})
	if err != nil || result.Status != gate.CheckStatusPass || result.EvidenceID != "evidence-1" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
