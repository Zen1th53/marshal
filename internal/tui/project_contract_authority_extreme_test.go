//go:build !governed

package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tmux"
)

func TestProjectContractAlwaysRequiresSeparateDecision(t *testing.T) {
	w, lease, _ := liveMCPFixture(t)
	bin := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(bin)
	t.Cleanup(tmux.ResetBinaryPath)
	id := occurrenceMCP(lease.id, "project-purpose-draft")
	// Checked contract submission will validate its complete body separately.
	// This fixture isolates routing: even an appointed live proposal is a DRAFT,
	// never its own normative acceptance decision.
	_, _, err := w.store.AdmitMCPProposal(t.Context(), w.projectID, store.MCPProposal{ID: id, Launch: lease.id, Owner: lease.owner, Provider: lease.provider, Submission: "project-purpose-draft", Payload: `{"action":"project-contract"}`})
	if err != nil {
		t.Fatal(err)
	}
	req := permission.Request{Kind: "project-contract", Object: "ARCH-project", Scope: w.projectID, Who: "Marshal", Reason: "Review project purpose and architecture", ProposalID: id, SourceOccurrence: id, SourceLaunch: lease.id}
	if !w.routeBuildPermission(t.Context(), req) {
		t.Fatal("contract draft was not routed")
	}
	w.permissions.mu.Lock()
	count := len(w.permissions.build.pending)
	for _, pending := range w.permissions.build.pending {
		if pending.request.Key() != req.Key() || pending.answer != nil || pending.launchID != lease.id {
			t.Error("contract question is not exact or was silently answered")
		}
	}
	w.permissions.mu.Unlock()
	if count != 1 {
		t.Fatalf("contract draft bypassed separate Decision: pending=%d", count)
	}
	p, err := w.store.MCPProposal(t.Context(), w.projectID, id)
	if err != nil || p.State != "admitted" {
		t.Fatalf("draft was applied before its separate answer: state=%q error=%v", p.State, err)
	}
}
