package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestMarshalDraftWatcherReportsRejectedDraftAndPreservesBytes(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(map[bool]string{false: "current", true: "stale"}[stale], func(t *testing.T) {
			w := recoveryChatWorkspace(t)
			m := w.marshalSession()
			root := w.providerRoot()
			watchID := "RUN-invalid-draft"
			current := watchID
			if stale {
				current = "RUN-current"
			}
			m.mu.Lock()
			m.runID = current
			m.service = w.runtime.Marshal()
			m.mu.Unlock()
			w.setMarshalPanel(&MarshalPanel{RunID: current, Note: "current planning"})
			writeRecoveryChatDraft(t, root)
			bad := []byte(`{"tasks":"private-untrusted-error-text"}`)
			path := filepath.Join(root, marshalDraftRelativePath)
			if err := os.WriteFile(path, bad, 0600); err != nil {
				t.Fatal(err)
			}
			// The watcher lives as long as the Marshal chat it serves.
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
			m.mu.Lock()
			done := m.draftDone
			m.mu.Unlock()
			deadline := time.Now().Add(4 * time.Second)
			for {
				if _, err := os.Stat(path + ".consumed"); err == nil && (stale || strings.Contains(w.marshalPanel().Note, "draft validation failed")) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("watcher did not report the rejected draft")
				}
				time.Sleep(50 * time.Millisecond)
			}
			// A rejection must not end draft import: the Marshal's corrected
			// draft is imported by the same watcher.
			select {
			case <-done:
				t.Fatal("watcher stopped after a rejected draft")
			case <-time.After(1500 * time.Millisecond):
			}
			retained, err := os.ReadFile(path + ".consumed")
			if err != nil || string(retained) != string(bad) {
				t.Fatalf("consumed evidence lost: %v", err)
			}
			panel := w.marshalPanel()
			if stale {
				if panel.RunID != current || panel.Note != "current planning" {
					t.Fatalf("stale watcher replaced current panel: %+v", panel)
				}
				return
			}
			if panel.RunID != watchID || !strings.Contains(panel.Note, "draft validation failed") {
				t.Fatalf("silent draft failure: %+v", panel)
			}
			if strings.Contains(panel.Note, "private-untrusted-error-text") {
				t.Fatal("raw draft error escaped")
			}
			m.mu.Lock()
			n := len(m.approvals)
			m.mu.Unlock()
			if n != 0 {
				t.Fatal("failed draft queued approval")
			}
		})
	}
}

// A draft for a run that already left drafting is imported as a fresh
// superseding run. Its rejection must still reach the watched run's panel;
// it used to be dropped because the fresh run ID was not the session's.
func TestMarshalDraftWatcherReportsRejectionForSupersedingDraft(t *testing.T) {
	w := recoveryChatWorkspace(t)
	m := w.marshalSession()
	root := w.providerRoot()
	watchID := "RUN-reviewing"
	service := w.runtime.Marshal()
	if _, err := service.Store.SetMarshalRun(t.Context(), service.ProjectID, watchID, marshal.Run{PlanID: "PLAN-reviewing", PlanVersion: 1, Settings: marshal.DefaultSettings(), State: marshal.Reviewing}, 0); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.runID = watchID
	m.service = service
	m.mu.Unlock()
	w.setMarshalPanel(&MarshalPanel{RunID: watchID, Note: "reviewing"})
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
	if panel := w.marshalPanel(); panel.RunID != watchID {
		t.Fatalf("rejection published on another run: %+v", panel)
	}
}
