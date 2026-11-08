//go:build governed

package app

import (
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestDefaultWorkerBriefOnlyUsableMemory(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	ctx := t.Context()
	now := time.Now().UTC()
	put := func(id string, lc model.MemoryLifecycle, mod func(*model.MemoryRecordV2)) {
		rec := model.MemoryRecordV2{ID: id, ProjectID: s.ProjectID, Title: id, Body: "body " + id, Kind: model.MemoryKindSemantic, Lifecycle: lc, Authority: model.AuthorityVerified,
			Scope: string(model.ScopeProject), ScopeID: s.ProjectID, Source: model.MemorySource{Kind: "runtime_service", Reference: "ref-" + id}, ObservedAt: now, ValidFrom: now.Add(-time.Hour), CreatedAt: now, UpdatedAt: now}
		if mod != nil {
			mod(&rec)
		}
		if err := s.Store.WriteMemoryV2(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	put("MEM-good-durable", model.MemoryDurable, nil)
	put("MEM-good-verified", model.MemoryVerified, nil)
	for _, lc := range []model.MemoryLifecycle{model.MemoryObserved, model.MemoryCandidate, model.MemoryStale, model.MemoryConflicted, model.MemorySuperseded, model.MemoryTombstoned, model.MemoryRejected} {
		put("MEM-bad-"+string(lc), lc, nil)
	}
	put("MEM-bad-supersededby", model.MemoryDurable, func(r *model.MemoryRecordV2) { r.SupersededBy = []string{"MEM-good-durable"} })
	put("MEM-bad-conflictids", model.MemoryDurable, func(r *model.MemoryRecordV2) { r.ConflictIDs = []string{"MEM-good-durable"} })
	past := now.Add(-time.Minute)
	put("MEM-bad-expired", model.MemoryDurable, func(r *model.MemoryRecordV2) { r.ValidTo = &past })

	var bc BriefContext
	if err := s.addWorkerMemory(ctx, "run", marshal.Run{}, marshal.Task{}, true, &bc); err != nil {
		t.Fatal(err)
	}
	if len(bc.Memory) != 2 {
		t.Fatalf("want exactly the durable and verified rows, got %d: %+v", len(bc.Memory), bc.Memory)
	}
	for _, m := range bc.Memory {
		if m.ID != "MEM-good-durable" && m.ID != "MEM-good-verified" {
			t.Fatalf("unusable memory reached worker brief: %s (%s)", m.ID, m.Lifecycle)
		}
		if m.Source.Reference != "ref-"+m.ID {
			t.Fatalf("provenance lost for %s: %+v", m.ID, m.Source)
		}
	}
}

func TestDefaultWorkerBriefUnusableRowsDoNotCrowdOutUsable(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	ctx := t.Context()
	base := time.Now().UTC()
	write := func(id string, lc model.MemoryLifecycle, at time.Time) {
		rec := model.MemoryRecordV2{ID: id, ProjectID: s.ProjectID, Title: id, Body: id, Kind: model.MemoryKindSemantic, Lifecycle: lc, Authority: model.AuthorityVerified,
			Scope: string(model.ScopeProject), ScopeID: s.ProjectID, Source: model.MemorySource{Kind: "runtime_service", Reference: id}, ObservedAt: at, ValidFrom: at, CreatedAt: at, UpdatedAt: at}
		if err := s.Store.WriteMemoryV2(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	write("MEM-old-usable", model.MemoryDurable, base.Add(-time.Hour))
	for i := 0; i < 15; i++ {
		write("MEM-new-cand-"+string(rune('a'+i)), model.MemoryCandidate, base.Add(time.Duration(i)*time.Second))
	}
	var bc BriefContext
	if err := s.addWorkerMemory(ctx, "run", marshal.Run{}, marshal.Task{}, true, &bc); err != nil {
		t.Fatal(err)
	}
	if len(bc.Memory) != 1 || bc.Memory[0].ID != "MEM-old-usable" {
		t.Fatalf("usable row crowded out by newer candidates: %+v", bc.Memory)
	}
}
