//go:build linux

package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiffScopesPTY(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepCrosscutEnvironment(t)
	sweepAgentCodexDouble(t)
	root := initProject(t, bin)
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", ".marshal/\n")
	ignore := exec.Command("git", "add", "-A")
	ignore.Dir = root
	if out, err := ignore.CombinedOutput(); err != nil {
		t.Fatalf("ignore: %v %s", err, out)
	}
	commit := exec.Command("git", "-c", "user.name=test", "-c", "user.email=test@example.invalid", "commit", "-qm", "ignore runtime metadata")
	commit.Dir = root
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v %s", err, out)
	}
	write("README.md", "staged-proof\n")
	cmd := exec.Command("git", "add", "--", "README.md")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("add: %v %s", err, out)
	}
	write("README.md", "unstaged-proof\n")
	write("new space 世界", "untracked-proof\n")
	s := startFrozenTUIInProject(t, 50, 200, bin, root, "tui")
	s.send("/diff")
	s.send("\x1b")
	s.send("\r")
	s.mustSee("staged: 1 files")
	s.mustSee("unstaged: 1 files")
	s.mustSee("staged-proof")
	s.send("\x1b[C")
	s.mustSee("unstaged-proof")
	s.send("\x1b[C")
	s.mustSee("untracked-proof")
	s.send("\x1b")
	s.send("/status")
	s.send("\x1b")
	s.send("\r")
	s.mustSee("CANONICAL STATUS DETAIL")
}

func TestDiffScopeCommandsAndCompletion(t *testing.T) {
	sweepCrosscutEnvironment(t)
	ws := NewWorkspace(nil, "diff", "diff")
	root := t.TempDir()
	init := exec.Command("git", "init", "-q", root)
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("init: %v %s", err, out)
	}
	ws.diffViewer = NewDiffViewer(nil, root)
	for _, scope := range []string{"staged", "unstaged", "untracked"} {
		out, err := ws.ExecuteCommand(context.Background(), "/diff "+scope)
		if err != nil || strings.Contains(out, "Diff error") || !ws.diffViewer.IsOpen() || ws.diffViewer.scope != scope {
			t.Fatalf("%s: %s %v", scope, out, err)
		}
		if rendered := strings.Join(ws.diffViewer.Render(100, 30), "\n"); !strings.Contains(rendered, "No changes in "+scope+" scope") {
			t.Fatal(rendered)
		}
		ws.diffViewer.Close()
		found := false
		for _, candidate := range ws.completer.ctx.Subcommands["/diff"] {
			if candidate == scope {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing completion %s", scope)
		}
		if !strings.Contains(NewCommandHandler(ws).helpText(), scope) {
			t.Fatalf("missing help %s", scope)
		}
	}
	for _, line := range []string{"/diff bad", "/diff staged extra"} {
		out, err := ws.ExecuteCommand(context.Background(), line)
		if err != nil || !strings.Contains(out, "Usage: /diff") {
			t.Fatalf("%s: %s %v", line, out, err)
		}
	}
	dv := NewDiffViewer(nil, t.TempDir())
	dv.LoadRawDiff("diff --git a/x b/x\n@@ -1 +1 @@\n+old\n")
	dv.active = true
	if err := dv.Refresh(); err == nil || dv.IsOpen() || len(dv.files) != 0 {
		t.Fatalf("failure left stale/clean overlay: %v", err)
	}
}

func TestF11ClosesDiffViewerOverlay(t *testing.T) {
	dv := NewDiffViewer(nil, t.TempDir())
	dv.active = true
	if !dv.IsOpen() {
		t.Fatal("expected diff viewer to be open")
	}
	handled := dv.HandleKey(KeyEvent{Type: KeyF11})
	if !handled {
		t.Fatal("expected KeyF11 to be handled by diff viewer")
	}
	if dv.IsOpen() {
		t.Fatal("expected diff viewer to be closed after KeyF11")
	}
}
