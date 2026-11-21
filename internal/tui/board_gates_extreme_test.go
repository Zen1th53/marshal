//go:build linux && !governed

package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// TestNoOwnerPlanGateInShippedBuild confirms the shipped build never offers
// the owner plan gate: the Marshal approves its own plan.
func TestNoOwnerPlanGateInShippedBuild(t *testing.T) {
	if ownerPlanGate {
		t.Fatal("ownerPlanGate must be false in the shipped build")
	}
	w := emptyBoardWorkspace(t)
	defer w.Close()
	w.planSource = fakePlanSource{}
	w.setMarshalPanel(draftingPanel("awaiting plan approval; /marshal approve"))
	if g := w.ownerGates(w.state.Marshal); len(g) != 0 {
		t.Fatalf("shipped build shows a plan gate: %+v", g)
	}
	text := frameText(w, 170, 40)
	for _, absent := range []string{"drafted plan waits for your approval", "[y] approve the plan", "[7] open the plan"} {
		if strings.Contains(text, absent) {
			t.Errorf("shipped Board shows removed plan gate %q:\n%s", absent, text)
		}
	}
}
