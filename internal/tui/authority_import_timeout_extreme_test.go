//go:build !governed

package tui

import (
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
)

// An unanswered review of imported session history keeps the episode a
// candidate: a decision timeout never promotes provider history to durable
// memory (it once auto-applied 121 imports of one session).
func TestExtremeUnansweredImportedHistoryStaysCandidate(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-extreme-import-timeout", false)
	recs, _, err := rt.ProposeContinuation(t.Context(), importer.SessionTranscript{SessionID: "codex-live", Provider: "codex", CWD: rt.ProjectRoot(), Messages: []importer.Message{{Role: "user", Content: "calc: add a parser"}}})
	if err != nil || len(recs) != 1 {
		t.Fatal(recs, err)
	}
	req := permission.Request{Kind: "memory", Object: recs[0].ID, Who: "Local request (unverified)", Reason: "Retain candidate"}
	// The proposal itself queued the review once; that delivery is the one
	// that went unanswered.
	w.permissions.queue.Take(false)
	w.permissions.mu.Lock()
	if w.permissions.outstanding == nil {
		w.permissions.outstanding = map[string]bool{}
	}
	w.permissions.outstanding[req.Key()] = true
	w.permissions.build.pending = map[string]*buildPendingDecision{"expired": {request: req, deadline: time.Now().Add(-time.Second), provider: "claude"}}
	w.permissions.mu.Unlock()
	if !w.routeBuildPermission(t.Context(), req) {
		t.Fatal("unanswered import review not handled")
	}
	rec, err := rt.Store().GetMemoryV2(t.Context(), rt.ProjectID(), recs[0].ID)
	if err != nil || rec.Lifecycle != model.MemoryCandidate {
		t.Fatalf("imported history promoted or lost on timeout: %+v %v", rec, err)
	}
	if w.permissions.outstanding[req.Key()] || len(w.permissions.queue.Take(false)) != 0 || w.permissions.build.redeliver[req.Key()] != nil {
		t.Fatal("kept candidate is still queued for re-delivery")
	}
	events, err := rt.Store().ListEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	kept := false
	for _, e := range events {
		if e.Type == "PERMISSION_DECIDED" && e.Data["object"] == req.Object {
			t.Fatalf("timeout decided imported history: %+v", e.Data)
		}
		kept = kept || (e.Type == "PERMISSION_UNANSWERED_KEPT_CANDIDATE" && e.Data["object"] == req.Object)
	}
	if !kept {
		t.Fatal("kept-candidate outcome not audited")
	}
}
