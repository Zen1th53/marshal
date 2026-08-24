package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/memory/working"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/protocol"
)

func TestA06RuntimeSubmitsProviderNeutralTypedHandoff(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ImportTasks(ctx, []model.Task{{ID: "TASK-T28", Title: "typed handoff", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	metadata := map[string]string{"task_id": "TASK-T28"}
	digest, err := evidence.CanonicalDigest(evidence.NodeTypeClaim, metadata)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.store.PutNode(ctx, evidence.Node{ID: "EVIDENCE-T28", Type: evidence.NodeTypeClaim, Digest: digest, Metadata: metadata, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	principal := protocol.Principal{ID: "AGENT-provider-neutral", Role: protocol.RoleDeveloper, Capabilities: []string{"handoff.create"}}
	submission := protocol.Submission{IdempotencyKey: "runtime-t28-handoff", Handoff: protocol.Handoff{ID: "HANDOFF-T28", Version: protocol.Version1, TaskID: "TASK-T28", FromAgent: "AGENT-provider-neutral", ToRole: protocol.RoleQA, Claims: map[string]string{"summary": "ready"}, EvidenceIDs: []protocol.EvidenceID{"EVIDENCE-T28"}, ChangedFiles: []string{"internal/protocol/engine.go"}, ContextDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}
	handoff, err := runtime.SubmitHandoff(ctx, principal, submission)
	if err != nil {
		t.Fatalf("SubmitHandoff: %v", err)
	}
	if handoff.Status != protocol.StatusAccepted || handoff.Version != protocol.Version1 {
		t.Fatalf("handoff = %#v, want accepted typed contract", handoff)
	}
	replayed, err := runtime.SubmitHandoff(ctx, principal, submission)
	if err != nil || replayed.ID != handoff.ID || replayed.Status != protocol.StatusAccepted {
		t.Fatalf("replayed handoff=%#v err=%v, want original accepted handoff", replayed, err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	consumer := protocol.Principal{ID: "AGENT-next-provider", Role: protocol.RoleQA, Capabilities: []string{"handoff.consume"}}
	consumed, err := restarted.ConsumeHandoff(ctx, consumer, handoff.ID)
	if err != nil {
		t.Fatalf("ConsumeHandoff after restart: %v", err)
	}
	if consumed.Status != protocol.StatusConsumed || consumed.ConsumedAt == nil || consumed.FromAgent != principal.ID {
		t.Fatalf("consumed handoff = %#v, want durable provider-neutral handoff", consumed)
	}
}

func TestMemoryHandoffUsesDurableTypedServiceAcrossRestart(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	const taskID = "TASK-MEMORY-HANDOFF"
	if _, err := runtime.ImportTasks(ctx, []model.Task{{ID: taskID, Title: "continue canonical memory work", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	sender := authz.Principal{ID: "agent-codex", Role: authz.Role{
		Name: "developer", Capabilities: []string{"handoff.create"}, Authorities: []authz.Authority{authz.AuthorityTaskPlan},
	}}
	grantTaskMemoryAccess(t, runtime, taskID, sender)
	if _, err := runtime.Memory().SetTaskSlot(ctx, sender, localProjectID, taskID, working.SlotPlanState, "verified next step", true); err != nil {
		t.Fatal(err)
	}
	memory, err := runtime.Memory().ExtractCandidate(ctx, sender, ExtractCandidateRequest{
		ProjectID: localProjectID, TaskID: taskID, Kind: model.MemoryKindFinding,
		Title: "Canonical handoff fact", Body: "task memory is persisted in SQLite", Scope: model.ScopeTask, ScopeID: taskID,
	})
	if err != nil {
		t.Fatal(err)
	}
	handoff, err := runtime.CompileAndSubmitHandoff(ctx, sender, HandoffCompileRequest{
		ProjectID: localProjectID, TaskID: taskID, SourceAgentID: sender.ID,
		TargetRole: string(protocol.RoleQA), CurrentBranch: "main",
	})
	if err != nil {
		t.Fatalf("compile and submit: %v", err)
	}
	if handoff.Status != protocol.StatusAccepted || !strings.Contains(handoff.Claims["memory_ids"], memory.ID) {
		t.Fatalf("unexpected submitted handoff: %+v", handoff)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	consumed, err := restarted.ConsumeHandoff(ctx, protocol.Principal{
		ID: "agent-gemini", Role: protocol.RoleQA, Capabilities: []string{"handoff.consume"},
	}, handoff.ID)
	if err != nil {
		t.Fatalf("consume after restart: %v", err)
	}
	if consumed.Status != protocol.StatusConsumed || !strings.Contains(consumed.Claims["memory_ids"], memory.ID) {
		t.Fatalf("handoff lost canonical references: %+v", consumed)
	}
	slots, err := restarted.Memory().ListTaskSlots(ctx, sender, localProjectID, taskID)
	if err != nil || len(slots) != 1 || slots[0].Value != "verified next step" {
		t.Fatalf("working memory lost across handoff restart: slots=%+v err=%v", slots, err)
	}
}
