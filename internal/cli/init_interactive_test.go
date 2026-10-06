//go:build linux

package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// workspaceExitWriter supplies a workspace command after setup has finished,
// so the confirmation reader cannot consume it ahead of the workspace.
type workspaceExitWriter struct {
	bytes.Buffer
	terminal *os.File
	sent     bool
}

func (w *workspaceExitWriter) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	if !w.sent && strings.Contains(string(p), "This project is set up for MARSHAL.") {
		w.sent = true
		_, _ = w.terminal.WriteString("/quit\n")
	}
	return n, err
}

func TestInitAndTUIOfferBaseline(t *testing.T) {
	t.Setenv("GIT_AUTHOR_NAME", "MARSHAL Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "MARSHAL Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.invalid")
	for _, action := range []string{"init", "tui"} {
		for _, repository := range []bool{false, true} {
			t.Run(action+map[bool]string{false: "/directory", true: "/unborn"}[repository], func(t *testing.T) {
				dir := t.TempDir()
				answers := "y\ny\n"
				if repository {
					testGit(t, dir, "init", "-q")
					if err := os.WriteFile(filepath.Join(dir, "staged.txt"), []byte("keep staged\n"), 0o600); err != nil {
						t.Fatal(err)
					}
					testGit(t, dir, "add", "staged.txt")
					answers = "y\n"
				}
				if action == "tui" {
					answers += "y\n"
				}
				master, slave := setupPTY(t)
				if _, err := master.WriteString(answers); err != nil {
					t.Fatal(err)
				}
				out := &workspaceExitWriter{terminal: master}
				var stderr bytes.Buffer
				if code := Execute(context.Background(), dir, []string{action}, slave, out, &stderr); code != 0 {
					t.Fatalf("%s code=%d stderr=%s stdout=%s", action, code, stderr.String(), out.String())
				}
				if !strings.Contains(out.String(), "Make an empty first commit as a baseline? [y/N]") {
					t.Fatalf("no commit confirmation: %s", out.String())
				}
				if !repository && !strings.Contains(out.String(), "Initialize a Git repository here? [y/N]") {
					t.Fatalf("no git confirmation: %s", out.String())
				}
				assertInitializedProject(t, dir)
				if files := testGit(t, dir, "ls-tree", "--name-only", "HEAD"); files != "" {
					t.Fatalf("baseline unexpectedly committed files: %s", files)
				}
				if repository && testGit(t, dir, "diff", "--cached", "--name-only") != "staged.txt" {
					t.Fatal("staged file was changed")
				}
			})
		}
	}
}

func TestInitAndTUIDeclinedBaseline(t *testing.T) {
	for _, action := range []string{"init", "tui"} {
		for _, repository := range []bool{false, true} {
			t.Run(action+map[bool]string{false: "/directory", true: "/unborn"}[repository], func(t *testing.T) {
				dir := t.TempDir()
				if repository {
					testGit(t, dir, "init", "-q")
				}
				master, slave := setupPTY(t)
				if _, err := master.WriteString("n\nn\n"); err != nil {
					t.Fatal(err)
				}
				var stdout, stderr bytes.Buffer
				code := Execute(context.Background(), dir, []string{action}, slave, &stdout, &stderr)
				if action == "init" && code == 0 {
					t.Fatal("init succeeded after declining baseline")
				}
				if _, err := os.Stat(filepath.Join(dir, ".marshal")); !os.IsNotExist(err) {
					t.Fatalf("declining created project state: %v", err)
				}
				if !repository {
					if _, err := os.Stat(filepath.Join(dir, ".git")); !os.IsNotExist(err) {
						t.Fatalf("declining created git: %v", err)
					}
				}
				assertNoInternalLeak(t, stdout.String()+stderr.String())
			})
		}
	}
}
