package shared_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/shared"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT95SharedArchivePeerProtection(t *testing.T) {
	arc := shared.NewArchive()
	ctx := context.Background()
	now := time.Now().UTC()

	// 1. Agent-1 contributes a finding
	rec1 := model.MemoryRecordV2{
		ID:        "MEM-SHARE-01",
		ProjectID: "PROJ-1",
		Kind:      model.MemoryKindFinding,
		Lifecycle: model.MemoryCandidate,
		Authority: model.AuthorityAgent,
		Title:     "Memory Leak in HTTP Client",
		Body:      "Discovered unclosed response body in client.go",
		Scope:     string(model.ScopeTeam),
		ScopeID:   "team-core",
		ObservedAt: now,
		ValidFrom:  now,
		Source:    model.MemorySource{Kind: "runtime", Reference: "task-1", AgentID: "agent-1"},
	}

	err := arc.Contribute(ctx, "agent-1", rec1)
	if err != nil {
		t.Fatalf("Contribute: %v", err)
	}

	// 2. Agent-2 attempting to mutate Agent-1's contribution must be rejected
	mutatedByPeer := rec1
	mutatedByPeer.Body = "Peer rewriting finding without permission"
	err = arc.UpdateContribution(ctx, "agent-2", mutatedByPeer)
	if !errors.Is(err, shared.ErrUnauthorizedMutation) {
		t.Fatalf("expected ErrUnauthorizedMutation for peer edit, got: %v", err)
	}

	// 3. Agent-1 updating their own unpromoted contribution succeeds
	mutatedByOwner := rec1
	mutatedByOwner.Body = "Refined: unclosed body specifically on 500 error code path"
	err = arc.UpdateContribution(ctx, "agent-1", mutatedByOwner)
	if err != nil {
		t.Fatalf("UpdateContribution by owner failed: %v", err)
	}

	// 4. Searching shared archive finds updated record
	results, err := arc.Search(ctx, "PROJ-1", "team-core", "HTTP Client")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].ID != "MEM-SHARE-01" {
		t.Fatalf("expected 1 finding from shared search, got: %+v", results)
	}
}

func TestT95ParallelContributions(t *testing.T) {
	arc := shared.NewArchive()
	ctx := context.Background()
	now := time.Now().UTC()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			agentID := "agent-worker"
			rec := model.MemoryRecordV2{
				ID:        model.MemoryRecordV2{}.ID, // unique ID per worker
				ProjectID: "PROJ-1",
				Kind:      model.MemoryKindSemantic,
				Lifecycle: model.MemoryCandidate,
				Title:     "Worker Note",
				Body:      "Completed parallel iteration",
				Scope:     string(model.ScopeTeam),
				ScopeID:   "team-core",
				ObservedAt: now,
				ValidFrom:  now,
				Source:    model.MemorySource{Kind: "runtime", Reference: "parallel", AgentID: agentID},
			}
			_ = arc.Contribute(ctx, agentID, rec)
		}(i)
	}
	wg.Wait()
}
