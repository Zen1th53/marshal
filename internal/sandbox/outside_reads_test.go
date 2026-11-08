package sandbox

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestBwrapOutsideReadsAreReadOnlyAndMasked(t *testing.T) {
	binary, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("bwrap unavailable")
	}
	backend := NewBwrap(binary)
	if capability := backend.Probe(context.Background()); !capability.Available {
		t.Skip(capability.Reason)
	}
	worktree := t.TempDir()
	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	secretDir := filepath.Join(outside, "keys")
	secretFile := filepath.Join(outside, "auth.json")
	if err := os.WriteFile(filepath.Join(outside, "note.txt"), []byte("outside-marker\nsecond\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(secretDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secretDir, "id"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secretFile, []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := "head -n1 " + outside + "/note.txt > copied; ls " + secretDir + " > keys; cat " + secretFile + " > auth; touch " + outside + "/written 2>/dev/null; echo $? > write-rc"
	spec, err := backend.Wrap(model.SandboxRequest{Worktree: worktree, OutsideReads: []model.OutsideRead{{
		Folder: outside, MaskDirs: []string{secretDir}, MaskFiles: []string{secretFile},
	}}}, []string{"/bin/sh", "-c", script})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(spec.Isolation.Reason, "outside folders read-only: "+outside) {
		t.Fatalf("isolation reason lacks outside evidence: %s", spec.Isolation.Reason)
	}
	command := exec.Command(spec.Path, spec.Args...)
	command.Env = spec.Env
	command.Dir = spec.Dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("bwrap: %v: %s", err, output)
	}
	for name, want := range map[string]string{"copied": "outside-marker\n", "keys": "", "auth": "", "write-rc": "1\n"} {
		data, err := os.ReadFile(filepath.Join(worktree, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != want {
			t.Fatalf("%s = %q, want %q", name, data, want)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "written")); !os.IsNotExist(err) {
		t.Fatalf("outside folder was writable: %v", err)
	}
}

func TestWrapRefusesOutsideReadsOverlappingTheWorktreeOrCredentials(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(parent, "work")
	if err := os.Mkdir(worktree, 0o700); err != nil {
		t.Fatal(err)
	}
	credentials := filepath.Join(parent, ".ssh")
	if err := os.Mkdir(credentials, 0o700); err != nil {
		t.Fatal(err)
	}
	other, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, read := range map[string]model.OutsideRead{
		"ancestor":   {Folder: parent},
		"worktree":   {Folder: worktree},
		"credential": {Folder: credentials},
		"root":       {Folder: "/"},
		"mask":       {Folder: other, MaskDirs: []string{parent}},
	} {
		if _, err := NewBwrap("/usr/bin/bwrap").Wrap(model.SandboxRequest{Worktree: worktree, OutsideReads: []model.OutsideRead{read}}, []string{"/bin/true"}); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("%s: err = %v, want invalid", name, err)
		}
	}
}
