//go:build governed && linux

package sandbox

import (
	"github.com/Zen1th53/marshal/internal/model"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCommunityDoesNotSelectAGYProjectMounts(t *testing.T) {
	root := t.TempDir()
	req := model.SandboxRequest{Worktree: root}
	spec, err := NewBwrap("/usr/bin/bwrap").Wrap(req, []string{"/bin/sh", "-c", "printf ordinary > ordinary.txt; printf home > /home/marshal/ordinary.txt"})
	if err != nil {
		t.Fatal(err)
	}
	for _, arg := range spec.Args {
		if arg == "/home/marshal/.gemini/config/projects" {
			t.Fatal("Community selected private native project mount")
		}
	}
	if out, err := exec.CommandContext(t.Context(), spec.Path, spec.Args...).CombinedOutput(); err != nil {
		t.Fatalf("ordinary writes failed: %v %s", err, out)
	}
	if raw, err := os.ReadFile(filepath.Join(root, "ordinary.txt")); err != nil || string(raw) != "ordinary" {
		t.Fatal("ordinary workspace write failed")
	}
	req.ReadOnlyBinds = []model.Bind{{Source: t.TempDir(), Target: "/home/marshal/.gemini/config"}}
	if _, err := NewBwrap("/usr/bin/bwrap").Wrap(req, []string{"/bin/true"}); err == nil {
		t.Fatal("ordinary provider directory acquired private config exception")
	}
}
