//go:build !governed

package tui

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestBrowserAcceptRecordsContractInstruction(t *testing.T) {
	w, _, cfg := liveMCPFixture(t)
	child := memoryChild(t, w.runtime.ProjectRoot(), &cfg)
	if reason := w.browserAcceptBlocked()(); reason != "The Marshal is still preparing the project summary." {
		t.Fatalf("Accept not blocked before a contract draft: %q", reason)
	}
	var inventory store.ProjectContractInventoryPage
	if err := json.Unmarshal([]byte(child.call(t, "marshal_project_contract_inventory", map[string]any{"limit": 1}, true)), &inventory); err != nil {
		t.Fatal(err)
	}
	draft := store.ProjectContractSubmission{Action: "project-contract", Contract: projectcontext.Contract{FormatVersion: projectcontext.FormatVersion, Project: w.runtime.ProjectContractBinding(), ID: "ARCH-browser", Revision: 1, Purpose: "Accepted in the browser", Components: []projectcontext.Component{{ID: "core", Responsibility: "Core", Paths: []string{"src"}}}, Invariants: []string{"Owner accepts in the browser"}, Required: []projectcontext.MemoryRef{}, ReviewedInventoryDigest: inventory.CompletenessDigest}}
	child.call(t, "marshal_project_contract_submit", map[string]any{"submission_id": "browser-contract", "proposal": draft}, true)
	if reason := w.browserAcceptBlocked()(); reason != "" {
		t.Fatalf("Accept blocked with a pending contract: %q", reason)
	}
	state := json.RawMessage(`{"architecture_id":"architecture-browser","revision":1,"digest":"` + strings.Repeat("a", 64) + `","builders":{"core":"codex"}}`)
	if err := w.beforeBrowserAccept()(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	w.browserAccepted(state)
	raw, err := os.ReadFile(inboxPath(w.runtime.ProjectRoot(), "marshal"))
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := draft.Contract.CanonicalDigest()
	for _, want := range []string{"The owner accepted architecture rev 1 in the browser.", "Builders: core=codex.", "instruction_ref=browser:", "contract_id=ARCH-browser; revision=1; digest=" + digest, "Continue: plan and launch now."} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("inbox lacks %q: %s", want, raw)
		}
	}
	if activityCount(w, "Architecture accepted in the browser (rev 1).") != 1 {
		w.mu.RLock()
		defer w.mu.RUnlock()
		t.Fatalf("Activity lacks the acceptance: %q", w.state.LastOutput)
	}
}

func TestBrowserAcceptRefusedWithoutContract(t *testing.T) {
	w, _, _ := liveMCPFixture(t)
	err := w.beforeBrowserAccept()(t.Context(), browserAcceptState(1))
	if err == nil || err.Error() != "The Marshal's project summary is not ready yet. Try again in a moment." {
		t.Fatalf("Accept without a contract: %v", err)
	}
	if raw, _ := os.ReadFile(inboxPath(w.runtime.ProjectRoot(), "marshal")); strings.Contains(string(raw), "instruction_ref=") {
		t.Fatalf("refused Accept told the Marshal: %s", raw)
	}
}
