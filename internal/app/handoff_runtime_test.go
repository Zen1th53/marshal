package app

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/evidence"
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
