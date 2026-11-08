//go:build governed

package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultOpenCodeMarshalRefused(t *testing.T) {
	w := recoveryChatWorkspace(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "opencode"), []byte("#!/bin/sh\necho 1.18.16\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := w.ExecuteCommand(t.Context(), "/marshal model opencode"); err == nil {
		t.Fatal("OpenCode accepted as Marshal")
	}
	if got := loadDefaultProvider(w.providerRoot()); got != "codex" {
		t.Fatalf("selection changed: %s", got)
	}
	m := w.marshalSession()
	m.mu.Lock()
	m.provider = "opencode"
	m.mu.Unlock()
	if _, _, _, err := w.marshalService(t.Context(), ""); err == nil {
		t.Fatal("OpenCode service accepted")
	}
}
