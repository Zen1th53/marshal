package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

// A draft only creates a run, so every stored run is superseded by a fresh
// run ID: reusing a stored drafting run's ID failed with "marshal_runs
// revision 1, expected 0" and the new draft was never imported.
func TestDraftSupersedesEveryStoredRun(t *testing.T) {
	if !draftSupersedesRun(nil) {
		t.Fatal("a stored run must be superseded by a fresh run")
	}
	if draftSupersedesRun(model.ErrNotFound) {
		t.Fatal("an unknown run ID takes the first draft itself")
	}
	if draftSupersedesRun(errors.New("store unavailable")) {
		t.Fatal("an unreadable run must not be treated as stored")
	}
}

// A second draft for a run still drafting (its plan can no longer be
// approved, for example after a Goal revision) is imported as a superseding
// run; its rejection still reaches the watched run's panel.
func TestMarshalDraftWatcherSupersedesDraftingRun(t *testing.T) {
	w := recoveryChatWorkspace(t)
	m := w.marshalSession()
	root := w.providerRoot()
	watchID := "RUN-drafting"
	service := w.runtime.Marshal()
	if _, err := service.Store.SetMarshalRun(t.Context(), service.ProjectID, watchID, marshal.Run{PlanID: "PLAN-drafting", PlanVersion: 1, Settings: marshal.DefaultSettings(), State: marshal.Drafting}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Snapshot(t.Context(), watchID); err != nil || !draftSupersedesRun(err) {
		t.Fatalf("stored drafting run is not superseded: %v", err)
	}
	m.mu.Lock()
	m.runID = watchID
	m.service = service
	m.mu.Unlock()
	w.setMarshalPanel(&MarshalPanel{RunID: watchID, Note: "drafting"})
	writeRecoveryChatDraft(t, root)
	path := filepath.Join(root, marshalDraftRelativePath)
	if err := os.WriteFile(path, []byte(`{"tasks":"invalid"}`), 0600); err != nil {
		t.Fatal(err)
	}
	w.tmuxMu.Lock()
	w.tmuxActiveWins["marshal-chat"] = &activeTmuxAgent{id: "marshal-chat", role: "marshal-chat"}
	w.tmuxMu.Unlock()
	defer func() {
		w.tmuxMu.Lock()
		delete(w.tmuxActiveWins, "marshal-chat")
		w.tmuxMu.Unlock()
	}()
	w.watchMarshalDraft(m, watchID, root, "codex", "")
	defer stopRecoveryDraftWatcher(w)
	deadline := time.Now().Add(4 * time.Second)
	for !strings.Contains(w.marshalPanel().Note, "draft validation failed") {
		if time.Now().After(deadline) {
			t.Fatalf("superseding draft rejection was silent: %+v", w.marshalPanel())
		}
		time.Sleep(50 * time.Millisecond)
	}
	panel := w.marshalPanel()
	if panel.RunID != watchID {
		t.Fatalf("rejection published on another run: %+v", panel)
	}
	if strings.Contains(panel.Note, "expected 0") {
		t.Fatalf("draft reused the stored run ID: %s", panel.Note)
	}
}
