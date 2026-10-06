package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCLIInstallationMountIncludesCompanion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	install := filepath.Join(home, ".codex", "packages", "release", "bin")
	if err := os.MkdirAll(install, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"codex", "codex-code-mode-host"} {
		if err := os.WriteFile(filepath.Join(install, name), []byte("executable"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(home, "codex-link")
	if err := os.Symlink(filepath.Join(install, "codex"), link); err != nil {
		t.Fatal(err)
	}
	binary, bind, err := cliInstallationMount(link)
	if err != nil {
		t.Fatal(err)
	}
	if binary != filepath.Join(install, "codex") || bind.Source != install || bind.Target != install {
		t.Fatalf("wrong mount: %s %+v", binary, bind)
	}
	if _, err := os.Stat(filepath.Join(bind.Source, "codex-code-mode-host")); err != nil {
		t.Fatal("companion not visible", err)
	}
	for _, dir := range []string{home, filepath.Join(home, ".codex"), filepath.Join(home, ".claude"), filepath.Join(home, ".aws"), filepath.Join(home, ".ssh"), filepath.Join(home, ".gemini")} {
		os.MkdirAll(dir, 0700)
		path := filepath.Join(dir, "cli")
		os.WriteFile(path, []byte("cli"), 0700)
		if _, _, err := cliInstallationMount(path); err == nil {
			t.Fatalf("unsafe directory accepted: %s", dir)
		}
	}
}
