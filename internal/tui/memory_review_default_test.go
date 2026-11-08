//go:build governed

package tui

import (
	"github.com/Zen1th53/marshal/internal/model"
	"strings"
	"testing"
	"time"
)

func TestDefaultMemoryCommandsKeepExistingInspectionContract(t *testing.T) {
	w, _ := realControlWorkspace(t, "SESSION-default-memory-contract", false)
	now := time.Now().UTC()
	r := model.MemoryRecordV2{ID: "MEM-default-command", ProjectID: w.projectID, Kind: model.MemoryKindSemantic, Lifecycle: model.MemoryCandidate, Authority: model.AuthorityAgent, Scope: "project", ScopeID: w.projectID, Body: "Default command source remains a candidate", Revision: 1, Source: model.MemorySource{Kind: "runtime_service"}, ObservedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now}
	if err := w.store.WriteMemoryV2(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"/memory read " + r.ID, "/memory promote " + r.ID + " Explicit source review"} {
		out, err := w.cmd.Handle(t.Context(), line)
		if err != nil || !strings.Contains(out, "Usage:") {
			t.Fatal("default private command behavior changed", line, out, err)
		}
	}
	current, err := w.store.GetMemoryV2(t.Context(), w.projectID, r.ID)
	if err != nil || current.Authority != r.Authority || current.Lifecycle != r.Lifecycle || current.Revision != r.Revision {
		t.Fatal("default command created private promotion authority", err)
	}
	if strings.Contains(w.cmd.helpText(), "promote <id>") {
		t.Fatal("default help advertises unsupported private promotion")
	}
}
