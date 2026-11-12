package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/project"
)

// TestLaunchScanLeavesOutMarshalsOwnFiles: a folder Marshal just set up holds
// only Marshal's own files, and the scan must read it as an empty project,
// not "a mixed project with 4 files".
func TestLaunchScanLeavesOutMarshalsOwnFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=T", "-c", "user.email=t@example.invalid"}, args...)...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "-b", "main")
	write(".gitignore", project.GitignoreHeader+"\n.env\n")
	git("add", ".gitignore")
	git("commit", "-q", "-m", "Initial commit")
	for _, name := range project.OwnedFiles {
		write(name, "x: 1\n")
	}
	git("add", ".")
	git("commit", "-q", "-m", "Initialize MARSHAL project")

	f, err := defaultScanFiles(t.Context(), root)
	if err != nil || f.Files != 0 {
		t.Fatalf("files part = %+v, %v; want 0 files", f, err)
	}
	block := projectScan{Language: f.Language, Files: f.Files}.briefingBlock(nil)
	if !strings.Contains(block, "- project: empty, no files yet") || strings.Contains(block, "mixed") {
		t.Fatalf("empty project read as:\n%s", block)
	}

	// The person's own files count, and so does a .gitignore they wrote.
	write("app.py", "pass\n")
	write(".gitignore", "/logs/\n")
	git("add", ".")
	git("commit", "-q", "-m", "mine")
	f, err = defaultScanFiles(t.Context(), root)
	if err != nil || f.Files != 2 || f.Language != "Python" {
		t.Fatalf("files part = %+v, %v; want Python, 2 files", f, err)
	}
}
