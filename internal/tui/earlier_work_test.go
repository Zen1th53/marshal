package tui

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/importer"
)

func TestHistoryProposalOnlyForFoundFolders(t *testing.T) {
	w, runtime := realControlWorkspace(t, "SESSION-earlier-work-gate")
	folder := runtime.EarlierWorkFolder("claude")
	w.launch.mu.Lock()
	w.launch.scan = projectScan{Sessions: []scanSessions{{Provider: "claude", Found: 2}}}
	w.launch.mu.Unlock()
	if !w.earlierWorkFolderFound(folder) || w.earlierWorkFolderFound(t.TempDir()) {
		t.Fatal("earlier-work folder gate did not restrict access to the found project folder")
	}
	w.observeMarshalProposals(importer.SessionTranscript{Messages: []importer.Message{{Role: "assistant", Content: `MARSHAL_PROPOSAL {"action":"continue","provider":"claude","path":"/outside/history"}`}}})
	w.mu.Lock()
	activity := w.state.LastOutput
	w.mu.Unlock()
	if !strings.Contains(activity, "History proposal deferred") {
		t.Fatalf("outside history proposal not deferred: %q", activity)
	}
}
