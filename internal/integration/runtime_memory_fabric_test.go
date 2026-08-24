package integration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/model"
)

func memoryReader(id string) authz.Principal {
	return authz.Principal{ID: id, Role: authz.Role{Name: "developer", Authorities: []authz.Authority{authz.AuthorityTaskPlan}}}
}

func TestRuntimeMemoryFabricCrossProviderRestartAndStaleness(t *testing.T) {
	ctx := context.Background()
	repo := runtimeIntegrationRepo(t)
	if _, err := app.Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}

	const taskID = "TASK-MEMORY-FABRIC"
	const head = "commit-current"
	var memoryID string
	func() {
		rt, err := app.Open(ctx, repo.Path())
		if err != nil {
			t.Fatal(err)
		}
		defer rt.Close()
		grantMemoryTaskAccess(t, rt, taskID,
			memoryReader("agent-gemini"), memoryReader("agent-ollama"), memoryReader("agent-claude"))

		captured, err := rt.Memory().CaptureOutcome(ctx, app.OutcomeCaptureRequest{
			ProjectID: "PROJECT-local", TaskID: taskID, TaskTitle: "Preserve SQLite as canonical memory",
			RunID: "RUN-CODEX-001", SessionID: "SESSION-CODEX", AgentID: "agent-codex",
			Provider: "codex", Status: "success", ExitStatus: 0, BaseCommit: "commit-base",
			HeadCommit: head, Branch: "feat/memory", EvidenceIDs: []string{"EVIDENCE-CODEX-001"},
		})
		if err != nil {
			t.Fatalf("agent A capture: %v", err)
		}
		memoryID = captured.ID

		res, err := rt.Memory().Recall(ctx, memoryReader("agent-gemini"), app.RecallRequest{
			ProjectID: "PROJECT-local", Query: "SQLite canonical", AllowedScopeIDs: []string{taskID},
			CurrentHead: head, CurrentBranch: "feat/memory", MaxRecords: 4, MaxBytes: 4096,
		})
		if err != nil {
			t.Fatalf("agent B recall: %v", err)
		}
		if len(res.Results) != 1 || res.Results[0].ID != memoryID || !strings.Contains(res.Context, memoryID) {
			t.Fatalf("agent B did not receive canonical context: %+v", res)
		}
		if len(res.Receipt.Decisions) != 1 || !res.Receipt.Decisions[0].Included {
			t.Fatalf("missing inclusion receipt: %+v", res.Receipt)
		}
	}()

	func() {
		rt, err := app.Open(ctx, repo.Path())
		if err != nil {
			t.Fatal(err)
		}
		defer rt.Close()
		res, err := rt.Memory().Recall(ctx, memoryReader("agent-ollama"), app.RecallRequest{
			ProjectID: "PROJECT-local", Query: "SQLite canonical", AllowedScopeIDs: []string{taskID},
			CurrentHead: head, CurrentBranch: "feat/memory", MaxRecords: 4, MaxBytes: 4096,
		})
		if err != nil {
			t.Fatalf("agent C recall after restart: %v", err)
		}
		if len(res.Results) != 1 || res.Results[0].ID != memoryID {
			t.Fatalf("durable candidate was lost across restart: %+v", res)
		}

		stale, err := rt.Memory().Recall(ctx, memoryReader("agent-claude"), app.RecallRequest{
			ProjectID: "PROJECT-local", Query: "SQLite canonical", AllowedScopeIDs: []string{taskID},
			CurrentHead: "commit-newer", CurrentBranch: "feat/memory", MaxRecords: 4, MaxBytes: 4096,
		})
		if err != nil {
			t.Fatalf("stale recall: %v", err)
		}
		if len(stale.Results) != 0 || len(stale.Receipt.Decisions) != 1 || !stale.Receipt.Decisions[0].Stale {
			t.Fatalf("repository-stale memory was not excluded: %+v", stale)
		}
	}()
}

func TestRuntimeMemoryFabricHardACLBeforeRanking(t *testing.T) {
	ctx := context.Background()
	repo := runtimeIntegrationRepo(t)
	if _, err := app.Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	rt, err := app.Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	now := time.Now().UTC()
	rec := model.MemoryRecordV2{
		ID: "MEM-PRIVATE-001", ProjectID: "PROJECT-local", Kind: model.MemoryKindDecision,
		Lifecycle: model.MemoryDurable, Confidence: model.ConfidenceVerified, Authority: model.AuthorityOperator,
		Title: "private exact needle", Body: "private exact needle must never rank for another agent",
		Scope: string(model.ScopeOperatorPrivate), ScopeID: "operator-a", ACLScope: "operator-a",
		Source:     model.MemorySource{Kind: "operator", Reference: "operator-a"},
		ObservedAt: now, IngestedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := rt.Store().WriteMemoryV2(ctx, rec); err != nil {
		t.Fatal(err)
	}

	res, err := rt.Memory().Recall(ctx, memoryReader("operator-b"), app.RecallRequest{
		ProjectID: "PROJECT-local", Query: "private exact needle", MaxRecords: 4, MaxBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != 0 || strings.Contains(res.Context, rec.Title) {
		t.Fatalf("private memory leaked into results or context: %+v", res)
	}
	if len(res.Receipt.Decisions) != 0 {
		t.Fatalf("store-level ACL must not disclose even the private memory ID in receipts: %+v", res.Receipt)
	}

	ownerRes, err := rt.Memory().Recall(ctx, memoryReader("operator-a"), app.RecallRequest{
		ProjectID: "PROJECT-local", Query: "private exact needle", MaxRecords: 4, MaxBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ownerRes.Results) != 1 || ownerRes.Results[0].ID != rec.ID {
		t.Fatalf("private memory owner could not recall its record: %+v", ownerRes)
	}
}

func TestRuntimeOutcomeCaptureRejectsSecretsBeforePersistence(t *testing.T) {
	ctx := context.Background()
	repo := runtimeIntegrationRepo(t)
	if _, err := app.Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	rt, err := app.Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	_, err = rt.Memory().CaptureOutcome(ctx, app.OutcomeCaptureRequest{
		ProjectID: "PROJECT-local", TaskID: "TASK-SECRET", RunID: "RUN-SECRET",
		TaskTitle: "leaked ghp_1234567890abcdefghijklmnopqrstuvwxyzAB", AgentID: "agent-codex",
		Provider: "codex", Status: "failed", ExitStatus: 1, HeadCommit: "commit-current",
	})
	if !errors.Is(err, security.ErrSecretDetected) {
		t.Fatalf("CaptureOutcome error = %v, want secret rejection", err)
	}
	if _, err := rt.Store().GetMemoryV2(ctx, "PROJECT-local", "MEM-RUN-RUN-SECRET"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("secret-bearing outcome persisted: %v", err)
	}
}

func TestRuntimeRecallDefaultsDenyUnspecifiedTaskScope(t *testing.T) {
	ctx := context.Background()
	repo := runtimeIntegrationRepo(t)
	if _, err := app.Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	rt, err := app.Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	now := time.Now().UTC()
	rec := model.MemoryRecordV2{
		ID: "MEM-OTHER-TASK", ProjectID: "PROJECT-local", Kind: model.MemoryKindDecision,
		Lifecycle: model.MemoryDurable, Confidence: model.ConfidenceVerified, Authority: model.AuthorityVerified,
		Title: "other task needle", Body: "task-scoped finding", Scope: string(model.ScopeTask), ScopeID: "TASK-OTHER",
		Source: model.MemorySource{Kind: "test", Reference: "TASK-OTHER"}, ObservedAt: now, IngestedAt: now,
		ValidFrom: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := rt.Store().WriteMemoryV2(ctx, rec); err != nil {
		t.Fatal(err)
	}

	denied, err := rt.Memory().Recall(ctx, memoryReader("agent-a"), app.RecallRequest{ProjectID: "PROJECT-local", Query: "other task needle"})
	if err != nil {
		t.Fatal(err)
	}
	if len(denied.Results) != 0 || len(denied.Receipt.Decisions) != 0 {
		t.Fatalf("unspecified task scope was not denied before ranking: %+v", denied)
	}

	if _, err := rt.Memory().Recall(ctx, memoryReader("agent-a"), app.RecallRequest{
		ProjectID: "PROJECT-local", Query: "other task needle", AllowedScopeIDs: []string{"TASK-OTHER"},
	}); !errors.Is(err, authz.ErrUnauthorized) {
		t.Fatalf("ungranted explicit task scope was accepted: %v", err)
	}
	grantMemoryTaskAccess(t, rt, "TASK-OTHER", memoryReader("agent-a"))
	allowed, err := rt.Memory().Recall(ctx, memoryReader("agent-a"), app.RecallRequest{
		ProjectID: "PROJECT-local", Query: "other task needle", AllowedScopeIDs: []string{"TASK-OTHER"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(allowed.Results) != 1 || allowed.Results[0].ID != rec.ID {
		t.Fatalf("explicit task scope was not recalled: %+v", allowed)
	}
}
