package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMarshalNativeLaunchCarriesArchitectureKnowledge(t *testing.T) {
	ws, rt := realControlWorkspace(t, "knowledge-launch", false)
	root := rt.ProjectRoot()
	for name, body := range map[string]string{"README.md": "# Atlas\n\nKeep engineering decisions.\n", "ARCHITECTURE.md": "# Architecture\n\nOnly the runtime owns durable state.\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"add", "README.md", "ARCHITECTURE.md"}, {"commit", "-m", "architecture fixture"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture: %v %s", err, output)
		}
	}
	brief := ws.marshalContinuityBriefing(root, "Marshal protocol.")
	if !strings.Contains(brief, "Only the runtime owns durable state") || !strings.Contains(brief, "engineering decisions") {
		t.Fatal("native Marshal launched without project purpose and architecture")
	}
}

func TestMarshalNativeLaunchPointsToKnowledgeWhenOverBudget(t *testing.T) {
	ws, rt := realControlWorkspace(t, "knowledge-budget", false)
	root := rt.ProjectRoot()
	brief := ws.marshalContinuityBriefing(root, strings.Repeat("p", marshalInlineKnowledgeMaximumBytes))
	if len(brief) > marshalInlineKnowledgeMaximumBytes+1024 || !strings.Contains(brief, "PROJECT KNOWLEDGE NOT INLINED") || !strings.Contains(brief, "project_context") {
		t.Fatalf("oversized launch knowledge was not replaced by an MCP pointer (%d bytes)", len(brief))
	}
}
