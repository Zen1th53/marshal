//go:build !governed

package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A Marshal turn (planning, amendment, review) whose observed project
// knowledge alone exceeds the required-context envelope is not refused: the
// knowledge moves to MCP recall and the mandatory packet stays complete.
func TestMarshalTurnMovesOversizedKnowledgeToRecall(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("host subprocess test requires python3")
	}
	for _, size := range []int{2 << 10, 80 << 10} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			outside := t.TempDir()
			capture := filepath.Join(outside, "prompt")
			binary := filepath.Join(outside, "host-owned-cli")
			script := fmt.Sprintf("#!%s\nimport json,sys\nprompt=sys.stdin.read() if sys.argv[-1]=='-' else sys.argv[-1]\nwith open(%q,'w') as f: f.write(prompt)\nprint(json.dumps({'type':'thread.started','thread_id':'host-test-session'}))\nprint(json.dumps({'type':'item.completed','item':{'type':'agent_message','text':'{\"ok\":true}'}}))\n", python, capture)
			if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			knowledge := "OBSERVED-KNOWLEDGE " + strings.Repeat("k", size)
			m := &MarshalCLI{Provider: "codex", Binary: binary, ProjectContext: func(context.Context, string, string) (string, error) { return knowledge, nil }}
			ctx := requiredMarshalTestContext(t, m)
			var output struct {
				OK bool `json:"ok"`
			}
			if err := m.turn(ctx, "Return the complete amended JSON task list.", `{"type":"object","properties":{"ok":{"type":"boolean"}}}`, &output); err != nil || !output.OK {
				t.Fatalf("turn refused: %v", err)
			}
			prompt, err := os.ReadFile(capture)
			if err != nil {
				t.Fatal(err)
			}
			inline := size < requiredProviderEnvelopeBytes
			if strings.Contains(string(prompt), "OBSERVED-KNOWLEDGE") != inline || strings.Contains(string(prompt), marshalKnowledgeRecallNote) == inline {
				t.Fatalf("size %d: knowledge inline=%v, want %v", size, !inline, inline)
			}
			if !strings.Contains(string(prompt), "Preserve reliable project execution") {
				t.Fatal("mandatory packet missing")
			}
		})
	}
}
