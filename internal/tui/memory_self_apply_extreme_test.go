//go:build !governed

package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/tmux"
)

// The Marshal approves memory in this edition, so asking the same model to
// approve its own write is a self-approval turn that provider safety layers
// refuse. Its own live candidate applies directly with recorded provenance;
// a candidate from any other author still asks the Marshal for a Decision.
func TestExtremeMarshalAuthoredMemoryAppliesWithoutSelfDecision(t *testing.T) {
	w, _, cfg := liveMCPFixture(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(filepath.Join(bin, "tmux"))
	t.Cleanup(tmux.ResetBinaryPath)
	c := memoryChild(t, w.runtime.ProjectRoot(), &cfg)
	r := decodeMemoryReceipt(t, c.call(t, "memory_remember", map[string]any{"submission_id": "self-apply", "title": "Marshal telemetry rule", "body": "Marshal builds keep ULTRA local and send no telemetry."}, true))
	if r.Persisted || r.State != "pending_permission" {
		t.Fatalf("receipt before runtime application: %+v", r)
	}
	batch := w.permissions.queue.Take(false)
	if len(batch) != 1 || batch[0].Who != "Marshal" || batch[0].SourceLaunch == "" {
		t.Fatalf("queue=%+v", batch)
	}
	own := batch[0]
	// Labels alone never establish authorship.
	for _, forge := range []func(*permission.Request){
		func(q *permission.Request) { q.SourceLaunch = "another-launch" },
		func(q *permission.Request) { q.Object = "MEM-other" },
		func(q *permission.Request) { q.Who = "worker-1" },
	} {
		q := own
		forge(&q)
		if _, ok := w.marshalAuthoredMemory(t.Context(), q); ok {
			t.Fatalf("forged request treated as Marshal-authored: %+v", q)
		}
	}
	if !w.routeBuildPermission(t.Context(), own) {
		t.Fatal("Marshal-authored memory not applied")
	}
	if len(w.permissions.build.pending) != 0 || !w.permissions.queue.Empty() || len(w.permissions.outstanding) != 0 {
		t.Fatalf("self-decision requested: pending=%d", len(w.permissions.build.pending))
	}
	saved := decodeMemoryReceipt(t, c.call(t, "marshal_memory_receipt", map[string]string{"submission_id": "self-apply"}, true))
	if !saved.Persisted || !saved.Indexed || saved.State != "applied" || saved.MemoryID != r.MemoryID {
		t.Fatalf("not durable: %+v", saved)
	}
	rec, err := w.store.GetMemoryV2(t.Context(), w.projectID, r.MemoryID)
	if err != nil || rec.Lifecycle == model.MemoryCandidate {
		t.Fatalf("canonical record: %+v %v", rec, err)
	}
	events, err := w.store.ListEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if e.Type == "PERMISSION_DECIDED" && e.Data["object"] == r.MemoryID {
			if e.Data["decided_by"] != marshalRuntimeDecider || e.Data["reason"] != marshalMemoryRuntimeReason || e.Data["allow"] != true || e.Data["source_occurrence"] != own.ProposalID {
				t.Fatalf("provenance: %+v", e.Data)
			}
			found = true
		}
		if e.Type == "PERMISSION_REQUESTED" && e.Data["object"] == r.MemoryID {
			t.Fatal("decision request sent for the Marshal's own memory")
		}
	}
	if !found {
		t.Fatal("no recorded runtime decision")
	}
	fresh := memoryChild(t, w.runtime.ProjectRoot(), nil)
	var recall app.RecallResponse
	raw := fresh.call(t, "memory_recall", map[string]string{"project_id": w.projectID, "query": "Marshal telemetry rule"}, true)
	if err := json.Unmarshal([]byte(raw), &recall); err != nil || len(recall.Results) != 1 || recall.Results[0].ID != r.MemoryID {
		t.Fatalf("not recallable across processes: %s %v", raw, err)
	}

	worker := memoryPermission(model.MemoryRecordV2{ID: "MEM-worker-note", ExtMeta: map[string]any{"provider": "codex"}, ObservedAt: time.Now(), Body: "worker observation"})
	worker.ProposalID, worker.Who = "worker-memory-1", "worker-1"
	if !w.routeBuildPermission(t.Context(), worker) {
		t.Fatal("worker candidate not routed")
	}
	if len(w.permissions.build.pending) != 1 {
		t.Fatalf("worker-authored memory must ask the Marshal: pending=%d", len(w.permissions.build.pending))
	}
	for _, p := range w.permissions.build.pending {
		if p.request.Object != "MEM-worker-note" || !strings.Contains(p.text, "Decision request") {
			t.Fatalf("pending=%+v", p)
		}
	}
}
