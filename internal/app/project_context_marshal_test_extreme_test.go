//go:build !governed

package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/projectid"
)

// Existing command-format tests run through the same real local owner and
// separately accepted contract as production. This does not bypass admission.
func requiredMarshalTestContext(t *testing.T, m *MarshalCLI) context.Context {
	t.Helper()
	// Preserve only the explicitly owned command-format executable. Every other
	// provider name fails locally, so constructor or later role fallback cannot
	// accidentally reach an installed authenticated native CLI.
	provider := normalizeProvider(m.Provider)
	selected := m.Binary
	var err error
	if selected == "" {
		selected, err = exec.LookPath(provider)
		if err != nil {
			t.Fatal(err)
		}
	}
	selected, err = filepath.Abs(selected)
	if err != nil {
		t.Fatal(err)
	}
	selected, err = filepath.EvalSymlinks(selected)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(selected)
	if err != nil || !info.Mode().IsRegular() || !strings.HasPrefix(selected, filepath.Clean(os.TempDir())+string(os.PathSeparator)) {
		t.Fatal("command fixture must own its executable before Runtime construction")
	}
	bin := t.TempDir()
	for _, name := range []string{"codex", "claude", "opencode", "agy"} {
		target := filepath.Join(bin, name)
		if name == provider {
			if err := os.Symlink(selected, target); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := os.WriteFile(target, []byte("#!/bin/sh\nexit 73\n"), 0700); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, name := range []string{"git", "sh", "grep", "cat"} {
		tool, err := exec.LookPath(name)
		if err != nil {
			tool = filepath.Join("/usr/bin", name)
			if _, err = os.Stat(tool); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Symlink(tool, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	r, b, _, _ := requiredContextFixture(t, true)
	request := planCreateRequest()
	request.SessionID, request.ProjectID = b.Session, projectid.ID(r.ProjectIdentity())
	if _, err := r.Plans().Create(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	control, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	m.runtime, m.ProjectID, m.Dir = r, r.ProjectIdentity(), r.ProjectRoot()
	return control.Context(t.Context())
}
