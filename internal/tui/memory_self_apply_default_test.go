//go:build governed

package tui

import (
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// Community keeps operator approval: even a candidate the live Marshal
// itself authored is never decided by the runtime; it waits for the operator.
func TestDefaultMarshalAuthoredMemoryAwaitsOperator(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-default-memory-operator", false)
	w.tmuxActiveWins = map[string]*activeTmuxAgent{"marshal-chat": {paneID: "%42", provider: "claude", sessionID: "chat", role: "marshal-chat", state: "working"}}
	req := memoryPermission(model.MemoryRecordV2{ID: "MEM-marshal-note", ExtMeta: map[string]any{"provider": "claude"}, ObservedAt: time.Now(), Body: "Marshal note"})
	req.ProposalID, req.SourceLaunch, req.SourceOccurrence, req.Who = "mcp:marshal-note", "launch", "mcp:marshal-note", "Marshal"
	if w.routeBuildPermission(t.Context(), req) {
		t.Fatal("Community memory candidate decided without the operator")
	}
	events, err := rt.Store().ListEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Type == "PERMISSION_DECIDED" && e.Data["object"] == "MEM-marshal-note" {
			t.Fatalf("runtime decided Community memory: %+v", e.Data)
		}
	}
}
