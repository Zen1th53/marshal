package codex

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestRequireAppServerConfigFreeRefusesUserConfiguration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("sandbox_mode = 'danger-full-access'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RequireAppServerConfigFree(); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("configured Codex home must be refused: %v", err)
	}
	if err := os.Remove(filepath.Join(home, "config.toml")); err != nil {
		t.Fatal(err)
	}
	if err := RequireAppServerConfigFree(); err != nil {
		t.Fatalf("config-free Codex home must be allowed: %v", err)
	}
}

func TestEnsureGovernedCodexHomeMirrorsAuthAndGuaranteesConfigFree(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("CODEX_HOME", "")

	hostCodex := filepath.Join(fakeHome, ".codex")
	if err := os.MkdirAll(hostCodex, 0o700); err != nil {
		t.Fatal(err)
	}
	// Write host auth.json, version.json and config.toml
	authContent := []byte(`{"tokens":{"access_token":"secret-token"}}`)
	if err := os.WriteFile(filepath.Join(hostCodex, "auth.json"), authContent, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hostCodex, "config.toml"), []byte("sandbox_mode = 'danger-full-access'\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	governed, err := EnsureGovernedCodexHome()
	if err != nil {
		t.Fatalf("EnsureGovernedCodexHome failed: %v", err)
	}
	// Verify config.toml is absent in governed directory
	if _, err := os.Stat(filepath.Join(governed, "config.toml")); !os.IsNotExist(err) {
		t.Fatal("governed CODEX_HOME must not contain config.toml")
	}
	// Verify auth.json was mirrored
	mirroredAuth, err := os.ReadFile(filepath.Join(governed, "auth.json"))
	if err != nil || string(mirroredAuth) != string(authContent) {
		t.Fatalf("mirrored auth.json mismatch: err=%v content=%s", err, string(mirroredAuth))
	}
	// Verify RequireAppServerConfigFree succeeds with governed home
	if err := RequireAppServerConfigFree(); err != nil {
		t.Fatalf("RequireAppServerConfigFree must succeed with governed home: %v", err)
	}
}

func TestGovernedCodexHomeUsesOwnerCODEXHOME(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	owner := t.TempDir()
	t.Setenv("CODEX_HOME", owner)
	if err := os.WriteFile(filepath.Join(owner, "auth.json"), []byte(`{"tokens":{"access_token":"owner-fixture"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(owner, "config.toml"), []byte("untrusted = true"), 0600); err != nil {
		t.Fatal(err)
	}
	target, err := EnsureGovernedCodexHome()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(target, "auth.json"))
	if err != nil || !strings.Contains(string(got), "owner-fixture") {
		t.Fatalf("owner's custom auth home ignored: %q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(target, "config.toml")); !os.IsNotExist(err) {
		t.Fatal("host configuration copied into governed home")
	}
	t.Setenv("CODEX_HOME", t.TempDir())
	if _, err := EnsureGovernedCodexHome(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "auth.json")); !os.IsNotExist(err) {
		t.Fatal("unsigned owner home reused an earlier account")
	}
}
