//go:build governed

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestDefaultWorkerStillRequiresSandboxEgress(t *testing.T) {
	binDir := t.TempDir()
	bin := filepath.Join(binDir, "codex")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := &Runtime{}
	// A networked Community worker cannot launch without a proxy. This also
	// covers systems where bubblewrap itself is unavailable.
	provider, _, err := r.resolveAdapter(t.Context(), "codex", model.Task{ID: "TASK-confined", Risk: model.R2}, t.TempDir(), "worker", true, "", "")
	if err == nil || provider != nil {
		t.Fatalf("network worker escaped Community sandbox: provider=%T err=%v", provider, err)
	}
}
