//go:build !governed

package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/tmux"
)

func TestExtremeProcess05ContinuationQuestionRequiresFreshDecisionAndExactRevision(t *testing.T) {
	isolateRequiredFixtureProviders(t)
	w, rt := realControlWorkspace(t, "SESSION-continuation-question", false)
	service := *rt.Marshal()
	draft, err := service.DraftFromProposal([]byte(`{"tasks":[{"id":"T1","title":"Review fixture","criteria":["fixture checked"],"paths":["README.md"],"depends_on":[],"worker":"codex","mode":"governed","checks":[{"command":"true","criteria":["fixture checked"]}]}]}`), "codex")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartPlanningFromDraft(t.Context(), "RUN-continuation", "fixture", draft, marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	m := w.marshalSession()
	m.mu.Lock()
	m.service, m.runID, m.provider = &service, "RUN-continuation", "codex"
	m.mu.Unlock()
	if err := w.queueMarshalRunProposal(marshalProposal{action: "resume", who: "Marshal"}); err != nil {
		t.Fatal(err)
	}
	batch := w.permissions.queue.Take(false)
	if len(batch) != 1 || batch[0].ProposalID != "" || batch[0].SourceLaunch != "" || batch[0].Object != "/marshal resume" {
		t.Fatalf("runtime question impersonated a model emission: %+v", batch)
	}
	fake := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(fake)
	t.Cleanup(tmux.ResetBinaryPath)
	w.tmuxActiveWins = map[string]*activeTmuxAgent{"marshal-chat": {paneID: "%42", provider: "codex"}}
	if !w.routeBuildPermission(t.Context(), batch[0]) {
		t.Fatal("question did not route")
	}
	w.permissions.mu.Lock()
	count, unanswered := len(w.permissions.build.pending), false
	for _, pending := range w.permissions.build.pending {
		unanswered = pending.answer == nil && pending.request.Key() == batch[0].Key()
	}
	w.permissions.mu.Unlock()
	if count != 1 || !unanswered {
		t.Fatal("runtime question auto-approved without a decision")
	}
	record, err := service.Store.GetMarshalRun(t.Context(), service.ProjectID, "RUN-continuation")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Store.SetMarshalRun(t.Context(), service.ProjectID, "RUN-continuation", record.Value, record.Revision); err != nil {
		t.Fatal(err)
	}
	if err := w.applyMarshalProposal(t.Context(), batch[0]); err == nil {
		t.Fatal("stale exact-revision question applied")
	}
}
