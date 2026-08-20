package integration

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestMemoryPersistsAcrossInterfacesAndRestart(t *testing.T) {
	repo := runtimeIntegrationRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	reader := authz.Principal{
		ID: "reader",
		Role: authz.Role{
			Name:        "reader",
			Authorities: []authz.Authority{authz.AuthorityTaskPlan},
		},
	}

	var memoryID string
	var projectID string

	// 1. Write memory through the canonical store, recall through MemoryService.
	func() {
		rt, err := app.Open(ctx, repo.Path())
		if err != nil {
			t.Fatal(err)
		}
		defer rt.Close()
		proj, err := rt.Store().Project(ctx)
		if err != nil {
			t.Fatal(err)
		}
		projectID = proj.ID

		now := time.Now().UTC()
		id, err := model.NewID("MEM-")
		if err != nil {
			t.Fatal(err)
		}
		memoryID = id
		if err := rt.Store().WriteMemoryV2(ctx, model.MemoryRecordV2{
			ID: id, ProjectID: projectID, Kind: model.MemoryKindDecision,
			Lifecycle: model.MemoryDurable, Confidence: model.ConfidenceVerified,
			Authority: model.AuthorityOperator, Title: "Ollama endpoint", Body: "127.0.0.1:11434",
			Scope: string(model.ScopeProject), ScopeID: projectID,
			ObservedAt: now, IngestedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now,
			Source: model.MemorySource{Kind: "test"},
		}); err != nil {
			t.Fatal(err)
		}

		svc := app.NewMemoryService(rt.Store())
		res, err := svc.Recall(ctx, reader, app.RecallRequest{ProjectID: projectID, Query: "Ollama"})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, item := range res.Results {
			if item.ID == memoryID {
				found = true
			}
		}
		if !found {
			t.Fatalf("recall through MemoryService did not find persisted record: %+v", res)
		}
	}()

	// 2. Restart runtime and recall again through the store.
	func() {
		rt, err := app.Open(ctx, repo.Path())
		if err != nil {
			t.Fatal(err)
		}
		defer rt.Close()

		rec, err := rt.Store().GetMemoryV2(ctx, projectID, memoryID)
		if err != nil {
			t.Fatalf("recall after restart: %v", err)
		}
		if rec.Title != "Ollama endpoint" || rec.Body != "127.0.0.1:11434" {
			t.Fatalf("unexpected record after restart: %+v", rec)
		}

		records, err := rt.Store().ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: projectID})
		if err != nil {
			t.Fatal(err)
		}
		if len(records) != 1 || records[0].ID != memoryID {
			t.Fatalf("unexpected record listing after restart: %+v", records)
		}
	}()
}
