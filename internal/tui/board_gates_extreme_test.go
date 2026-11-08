//go:build !governed

package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/store"
)

// writeGateANS writes the frame for tools/e2e/ansi_png.py when
// MARSHAL_GATE_ANS names a directory.
func writeGateANS(t *testing.T, w *Workspace, name string, cols, rows int) {
	t.Helper()
	dir := os.Getenv("MARSHAL_GATE_ANS")
	if dir == "" {
		return
	}
	lines, _, _ := w.frameLinesFull(cols, rows)
	if err := os.WriteFile(filepath.Join(dir, name+".ans"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// End to end on a real store: the live Marshal submits a project contract
// through MCP, the Board reads it as the owner's gate (contract id,
// revision, purpose, NEEDS YOU); the owner's "accept architecture" in the
// chat closes it. A drafted plan saved in the store is the plan's gate.
func TestOwnerGatesFromTheSeededStore(t *testing.T) {
	w, _, cfg := liveMCPFixture(t)
	w.screen = NewScreen(nil)
	w.homeBoard = true
	w.theme = NewTheme(ThemeDefault, true, true)
	t.Setenv("COLORTERM", "truecolor")
	child := memoryChild(t, w.runtime.ProjectRoot(), &cfg)
	ctx := t.Context()

	w.refreshGates(ctx)
	if g := w.ownerGates(nil); len(g) != 0 {
		t.Fatalf("gates before any submission: %+v", g)
	}
	var inventory store.ProjectContractInventoryPage
	if err := json.Unmarshal([]byte(child.call(t, "marshal_project_contract_inventory", map[string]any{"limit": 1}, true)), &inventory); err != nil {
		t.Fatal(err)
	}
	draft := store.ProjectContractSubmission{Action: "project-contract", Contract: projectcontext.Contract{FormatVersion: projectcontext.FormatVersion, Project: w.runtime.ProjectContractBinding(), ID: "hello-uz", Revision: 1, Purpose: "Build hello-uz: a tiny standard-library Go CLI whose hellouz NAME invocation prints Salom, NAME!, with a table-driven test.", Components: []projectcontext.Component{{ID: "cli", Responsibility: "main package prints Salom, NAME!", Paths: []string{"main.go"}}}, Invariants: []string{"standard library only"}, Required: []projectcontext.MemoryRef{}, ReviewedInventoryDigest: inventory.CompletenessDigest}}
	child.call(t, "marshal_project_contract_submit", map[string]any{"submission_id": "hello-uz-contract-01", "proposal": draft}, true)
	w.refreshGates(ctx)
	text := frameText(w, 120, 34)
	for _, want := range []string{"NEEDS YOU", "contract hello-uz rev 1 waits for your acceptance", "Build hello-uz", "[y] accept architecture", "[b] open details"} {
		if !strings.Contains(text, want) {
			t.Errorf("Board misses %q:\n%s", want, text)
		}
	}
	writeGateANS(t, w, "architecture-gate", 120, 34)

	ownerAcceptsArchitecture(t, w)
	w.refreshGates(ctx)
	if g := w.ownerGates(nil); len(g) != 0 {
		t.Fatalf("the owner accepted; gates %+v", g)
	}

	run := marshal.Run{State: marshal.Drafting, PlanID: "PLAN-01d4bcc2-r3", PlanVersion: 1, Tier: marshal.Ultra, Settings: marshal.DefaultSettings(), Tasks: []marshal.Task{{PlanTaskID: "hello-cli", Title: "implement and test hello-uz", Worker: "codex", Files: []string{"main.go", "main_test.go"}, Criteria: []string{"go test PASS"}}}}
	const runID = "RUN-1794117001273523713"
	if err := w.runtime.Store().SaveMarshalState(ctx, w.projectID, runID, run, 0, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	rec, err := w.runtime.Store().GetMarshalRun(ctx, w.projectID, runID)
	if err != nil {
		t.Fatal(err)
	}
	w.setMarshalPanel(newMarshalPanel(runID, "codex", rec.Value, "awaiting plan approval; /marshal approve"))
	text = frameText(w, 120, 34)
	for _, want := range []string{"NEEDS YOU", "drafted plan waits for your approval · 1 task · run R", "[y] approve the plan", "[7] open the plan"} {
		if !strings.Contains(text, want) {
			t.Errorf("Board misses %q:\n%s", want, text)
		}
	}
	writeGateANS(t, w, "plan-gate", 120, 34)
}
