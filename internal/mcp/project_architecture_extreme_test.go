//go:build !governed

package mcp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
)

func TestExtremeProjectArchitectureFollowsExistingAndNewProjects(t *testing.T) {
	for _, architecture := range []bool{true, false} {
		t.Run(map[bool]string{true: "existing", false: "new"}[architecture], func(t *testing.T) {
			root := runtimeRepo(t).Path()
			files := map[string]string{"README.md": "# Atlas\n\nKeep the team's engineering decisions.\n"}
			if architecture {
				files["ARCHITECTURE.md"] = "# Architecture\n\nThe runtime owns state; providers propose work.\n"
			}
			for name, body := range files {
				if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for _, args := range [][]string{{"add", "."}, {"commit", "-m", "project context fixture"}} {
				cmd := exec.Command("git", args...)
				cmd.Dir = root
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("fixture: %v %s", err, output)
				}
			}
			if _, err := app.Bootstrap(t.Context(), root); err != nil {
				t.Fatal(err)
			}
			rt, err := app.Open(t.Context(), root)
			if err != nil {
				t.Fatal(err)
			}
			read := func(runtime *app.Runtime) string {
				t.Helper()
				text, err := NewExtremeServer(runtime, "marshal", "").callTool(context.Background(), NewExtremeServer(runtime, "marshal", "").defaultCaller(), "project_context", map[string]any{"refresh": true, "max_bytes": float64(32768)})
				if err != nil {
					t.Fatal(err)
				}
				var packet map[string]any
				if err := json.Unmarshal([]byte(text), &packet); err != nil {
					t.Fatal(err)
				}
				return text
			}
			first := read(rt)
			if !strings.Contains(first, "engineering decisions") {
				t.Fatal("project purpose omitted")
			}
			if architecture && !strings.Contains(first, "runtime owns state") {
				t.Fatal("architecture omitted")
			}
			if !architecture && !strings.Contains(first, "needs_architecture") {
				t.Fatal("new project invents an architecture")
			}
			if err := rt.Close(); err != nil {
				t.Fatal(err)
			}
			rt, err = app.Open(t.Context(), root)
			if err != nil {
				t.Fatal(err)
			}
			defer rt.Close()
			if next := read(rt); !strings.Contains(next, "engineering decisions") {
				t.Fatal("context lost after restart")
			}
			if architecture {
				if err := os.WriteFile(filepath.Join(root, "ARCHITECTURE.md"), []byte("# Architecture\n\nThe database owns state; workers only deliver artifacts.\n"), 0600); err != nil {
					t.Fatal(err)
				}
				next := read(rt)
				if !strings.Contains(next, "database owns state") || strings.Contains(next, "runtime owns state") {
					t.Fatal("old architecture served after edit")
				}
			}
		})
	}
}
