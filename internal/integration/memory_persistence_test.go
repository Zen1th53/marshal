package integration

import (
	"context"
	"strings"
	"testing"

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

	// 1. Record and promote a candidate in the first run.
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

		operator := authz.Principal{ID: "operator", Role: authz.Role{
			Name: "operator", Authorities: []authz.Authority{authz.AuthorityPolicyAdmin},
		}}
		candidate, err := rt.Memory().Remember(ctx, operator, app.RememberRequest{
			ProjectID: projectID, Kind: model.MemoryKindDecision,
			Title: "Ollama endpoint", Body: "127.0.0.1:11434",
		})
		if err != nil {
			t.Fatal(err)
		}
		memoryID = candidate.ID
		before, err := rt.Memory().Recall(ctx, reader, app.RecallRequest{
			ProjectID: projectID, Query: "Ollama", RunID: "run-one",
		})
		if err != nil || len(before.Results) != 0 || before.Receipt.ConsumedBytes != 0 {
			t.Fatalf("unreviewed candidate delivered: %+v, err=%v", before, err)
		}
		if _, err := rt.Memory().Promote(ctx, operator, app.PromoteRequest{
			ProjectID: projectID, MemoryID: memoryID, ScopeID: projectID,
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

	// 2. A different agent in a later run receives the promoted body and receipt.
	func() {
		rt, err := app.Open(ctx, repo.Path())
		if err != nil {
			t.Fatal(err)
		}
		defer rt.Close()

		laterAgent := reader
		laterAgent.ID = "later-agent"
		response, err := rt.Memory().Recall(ctx, laterAgent, app.RecallRequest{
			ProjectID: projectID, Query: "Ollama", RunID: "run-two",
			AllowedScopeIDs: []string{projectID}, ExcludeSessionHistory: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(response.Results) != 1 || response.Results[0].ID != memoryID ||
			response.Results[0].Lifecycle != model.MemoryDurable ||
			!strings.Contains(response.Context, "127.0.0.1:11434") || response.Receipt.ConsumedBytes == 0 {
			t.Fatalf("later agent did not receive promoted memory: %+v", response)
		}
		receipt, err := rt.Store().GetRetrievalReceipt(ctx, projectID, response.Receipt.ReceiptID)
		if err != nil {
			t.Fatal(err)
		}
		if receipt.CallerID != laterAgent.ID || receipt.RunID != "run-two" || receipt.ConsumedBytes == 0 ||
			!strings.Contains(receipt.DecisionsJSON, `"included":true`) {
			t.Fatalf("delivery receipt = %+v", receipt)
		}

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
