package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/tmux"
)

func TestNativeStateDoesNotAdoptForeignVariant(t *testing.T) {
	root := t.TempDir()
	foreign := filepath.Join(root, statepath.OtherDirName)
	if err := os.MkdirAll(foreign, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(foreign, "default-provider"), []byte("claude\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if loadDefaultProvider(root) != "" {
		t.Fatal("foreign provider preference adopted")
	}
	if err := saveDefaultProvider(root, "codex"); err != nil {
		t.Fatal(err)
	}
	if loadDefaultProvider(root) != "codex" {
		t.Fatal("own provider preference missing")
	}
	data, err := os.ReadFile(filepath.Join(foreign, "default-provider"))
	if err != nil || string(data) != "claude\n" {
		t.Fatal("foreign provider preference changed")
	}
	if err := os.Remove(defaultProviderPath(root)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(foreign, "default-provider"), defaultProviderPath(root)); err != nil {
		t.Fatal(err)
	}
	if loadDefaultProvider(root) != "" {
		t.Fatal("native read followed alias")
	}
	if err := saveDefaultProvider(root, "opencode"); err == nil {
		t.Fatal("native write followed alias")
	}
	data, err = os.ReadFile(filepath.Join(foreign, "default-provider"))
	if err != nil || string(data) != "claude\n" {
		t.Fatal("aliased foreign preference changed")
	}
}

func TestNativeStateTmuxBindingRefusesOtherVariant(t *testing.T) {
	root := t.TempDir()
	otherName := "marshal-extreme"
	a := &activeTmuxAgent{id: "worker", provider: "codex", paneID: "%777", role: "worker"}
	if err := saveAgentRecord(root, otherName+"-project-"+tmux.ProjectHash(root), a); err == nil {
		t.Fatal("foreign tmux session binding accepted")
	}
	if err := saveAgentRecord(root, tmux.SessionName(root), a); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAgentRecord(root, a.paneID); err != nil {
		t.Fatal(err)
	}
}
