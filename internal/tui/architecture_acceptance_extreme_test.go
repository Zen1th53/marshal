//go:build !governed

package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/Zen1th53/marshal/internal/architecturereview"
	"github.com/Zen1th53/marshal/internal/memory/importer"
)

func TestArchitectureInstructionRequiresBoundOperatorChat(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-architecture-instruction", false)
	graph := json.RawMessage(`{"title":"Service","purpose":"Serve requests","components":[{"id":"api","name":"API","responsibility":"Serve","technology":"Go"}],"connections":[],"constraints":[]}`)
	layouts, err := architecturereview.GenericLayouts(graph)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := architecturereview.New(architecturereview.Config{Project: rt.ProjectIdentity(), ArchitectureID: "architecture-chat", StateDir: filepath.Join(rt.StateDirectory(), "architecture", "review"), Proposal: graph, Layouts: layouts}); err != nil {
		t.Fatal(err)
	}
	w.tmuxMu.Lock()
	w.tmuxActiveWins["marshal-chat"] = &activeTmuxAgent{role: "marshal-chat", sessionID: "bound-chat", state: "working"}
	w.tmuxMu.Unlock()
	tr := importer.SessionTranscript{SessionID: "unbound-chat", Messages: []importer.Message{{Role: "user", Content: "accept architecture"}}}
	w.observeAuthenticatedMarshalProposals(tr)
	tr.SessionID = "bound-chat"
	tr.Messages[0].Role = "assistant"
	w.observeAuthenticatedMarshalProposals(tr)
	tr.Messages[0].Role = "user"
	w.observeMarshalProposals(tr)
	if raw, _ := os.ReadFile(inboxPath(rt.ProjectRoot(), "marshal")); regexp.MustCompile(`instruction_ref=operator:`).Match(raw) {
		t.Fatal("unbound or model text became an operator instruction")
	}
	w.observeAuthenticatedMarshalProposals(tr)
	raw, err := os.ReadFile(inboxPath(rt.ProjectRoot(), "marshal"))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`instruction_ref=(operator:[0-9a-f]+);`).FindSubmatch(raw)
	if len(match) != 2 {
		t.Fatalf("recorded instruction reference not delivered: %s", raw)
	}
	selection, _, err := w.store.ObserveMarshalArchitecture(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ref := string(match[1])
	if _, err := w.store.CommitMarshalArchitectureAcceptance(t.Context(), w.projectID, "foreign-chat", ref, "proposal-1", selection); err == nil {
		t.Fatal("instruction crossed sessions")
	}
	proposal := fmt.Sprintf(`MARSHAL_PROPOSAL {"action":"accept-architecture","id":%q,"revision":%q,"digest":%q,"instruction_ref":%q}`, selection.ArchitectureID, fmt.Sprint(selection.Revision), selection.Digest, ref)
	tr.Messages = append(tr.Messages, importer.Message{Role: "assistant", Content: proposal})
	w.observeAuthenticatedMarshalProposals(tr)
	accepted, err := rt.ArchitectureAcceptance(t.Context())
	if err != nil || !accepted.ModelSelfApproval || accepted.OperatorInstructionRef != ref {
		t.Fatalf("bound operator instruction: %+v %v", accepted, err)
	}
	if err := rt.RequireArchitectureAcceptance(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Polling the same transcript cannot rebind its instruction to new source.
	if err := os.WriteFile(filepath.Join(rt.ProjectRoot(), "new.go"), []byte("package changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	w.observeAuthenticatedMarshalProposals(tr)
	changed, _, err := w.store.ObserveMarshalArchitecture(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.store.CommitMarshalArchitectureAcceptance(t.Context(), w.projectID, "bound-chat", ref, "proposal-2", changed); err == nil {
		t.Fatal("replayed instruction authorized new source")
	}
}
