package app

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// Lexical candidates used to come only from the rarest query word's posting.
// A durable record matching enough other words (the admission rule) but not
// that rare word was never considered, so multi-word recall returned nothing.
func TestRecallCandidatesCompleteForAdmissionRule(t *testing.T) {
	ctx := context.Background()
	rt, svc := openTestMemoryService(t)
	project := rt.ProjectID()
	now := time.Now().UTC()
	write := func(id, title, body string) {
		t.Helper()
		rec := model.MemoryRecordV2{ID: id, ProjectID: project, Kind: model.MemoryKindSemantic, Lifecycle: model.MemoryDurable, Confidence: model.ConfidenceInferred, Authority: model.AuthorityAgent, Title: title, Body: body, Scope: string(model.ScopeProject), ScopeID: project, Source: model.MemorySource{Kind: "test", Reference: "candidates"}, ObservedAt: now, IngestedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now}
		if err := rt.Store().WriteMemoryV2(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	write("MEM-owner-one", "Owner decision", "Owner decision on the architecture of private builds.")
	write("MEM-owner-two", "Second owner decision", "The owner made a decision about plugin architecture.")
	write("MEM-roadmap", "Phase plan", "Roadmap phases A to D.")
	if err := svc.RebuildProjections(ctx, project); err != nil {
		t.Fatal(err)
	}
	// "roadmap" is the rarest word; both owner records match 3 of 4 words.
	res, err := svc.Recall(ctx, testPrincipal("reader"), RecallRequest{ProjectID: project, Query: "owner decision architecture roadmap"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, item := range res.Results {
		got[item.ID] = true
	}
	if !got["MEM-owner-one"] || !got["MEM-owner-two"] || got["MEM-roadmap"] {
		t.Fatalf("results=%v decisions=%+v", recallItemIDs(res.Results), res.Receipt.Decisions)
	}
}
