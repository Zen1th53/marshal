//go:build !governed

package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFreshHarnessResolvesInstalledCodexCatalogBeforeProbe(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	r, err := Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	bin := t.TempDir()
	script := `#!/bin/sh
if [ "$1" = --version ]; then echo 1.0.0; exit 0; fi
if [ "$1" = debug ] && [ "$2" = models ]; then
 echo '{"models":[{"slug":"catalog-current","is_default":true}]}'
 exit 0
fi
exit 93
`
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	selected, err := r.harnessProbeModel(t.Context(), "codex", "")
	if err != nil || selected != "catalog-current" {
		t.Fatalf("fresh catalog unresolved: %q %v", selected, err)
	}
}
