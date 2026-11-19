package store

import (
	"context"
	"testing"
)

func TestMarshalProposalLineResolutionAndMarkers(t *testing.T) {
	s := openTestStore(t)
	marshalTestProject(t, s)
	ctx := context.Background()

	line := `MARSHAL_PROPOSAL {"action":"ask","question":"Keep branch?","choices":"[{\"key\":\"enter\",\"label\":\"yes\"},{\"key\":\"n\",\"label\":\"no\"}]"}`
	if _, err := s.AdmitMarshalProposal(ctx, "p", "occ-1", line); err != nil {
		t.Fatal(err)
	}
	// Still pending: not yet resolved.
	if got, err := s.HasResolvedMarshalProposalLine(ctx, "p", line); err != nil || got {
		t.Fatalf("pending line reported resolved: %v %v", got, err)
	}
	if err := s.ResolveMarshalProposal(ctx, "p", "occ-1", "applied"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.HasResolvedMarshalProposalLine(ctx, "p", line); err != nil || !got {
		t.Fatalf("resolved line not detected: %v %v", got, err)
	}

	// Markers upsert cleanly and never show up in PendingMarshalProposals.
	if _, ok, err := s.MarshalProposalMarker(ctx, "p", "card:latest:requirements"); err != nil || ok {
		t.Fatalf("missing marker reported present: %v %v", ok, err)
	}
	if err := s.RecordMarshalProposalMarker(ctx, "p", "card:latest:requirements", "ws-1\t100", "recorded"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordMarshalProposalMarker(ctx, "p", "card:latest:requirements", "ws-1\t200", "recorded"); err != nil {
		t.Fatal(err)
	}
	val, ok, err := s.MarshalProposalMarker(ctx, "p", "card:latest:requirements")
	if err != nil || !ok || val != "ws-1\t200" {
		t.Fatalf("marker = %q, %v, %v; want ws-1\\t200", val, ok, err)
	}
	pending, err := s.PendingMarshalProposals(ctx, "p")
	if err != nil || len(pending) != 0 {
		t.Fatalf("markers leaked into pending proposals: %+v %v", pending, err)
	}
}
