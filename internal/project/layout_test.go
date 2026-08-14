package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDiscoverUsesMarshalRuntimeDirectory(t *testing.T) {
	repo := t.TempDir()
	for _, args := range [][]string{{"init"}, {"config", "user.name", "Test"}, {"config", "user.email", "test@example.invalid"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "README.md"}, {"commit", "-m", "initial"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}

	layout, err := Discover(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := layout.RuntimeDir, filepath.Join(repo, ".marshal"); got != want {
		t.Fatalf("RuntimeDir = %q, want %q", got, want)
	}
}

func TestCompareSemver(t *testing.T) {
	tests := []struct {
		v1, v2   string
		expected int // <0 if v1 < v2, 0 if equal, >0 if v1 > v2
	}{
		{"0.9.0", "0.10.0", -1},
		{"0.10.0", "0.9.0", 1},
		{"0.10.2", "0.10.10", -1},
		{"0.146.0-x86_64-unknown-linux-musl", "0.145.0-x86_64-unknown-linux-musl", 1},
		{"0.10.0", "0.10.0", 0},
	}
	for _, tt := range tests {
		got := compareSemver(tt.v1, tt.v2)
		if (got < 0 && tt.expected >= 0) || (got > 0 && tt.expected <= 0) || (got == 0 && tt.expected != 0) {
			t.Errorf("compareSemver(%q, %q) = %d, expected sign %d", tt.v1, tt.v2, got, tt.expected)
		}
	}
}

func TestFindCodexBinarySemverFallback(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	t.Setenv("PATH", t.TempDir()) // Ensure PATH has no codex

	releasesDir := filepath.Join(tempHome, ".codex", "packages", "standalone", "releases")
	for _, ver := range []string{"0.9.0", "0.10.0", "0.10.2", "0.10.10"} {
		dir := filepath.Join(releasesDir, ver, "bin")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	found, err := FindBinary("codex")
	if err != nil {
		t.Fatalf("FindBinary(codex) error: %v", err)
	}
	expected := filepath.Join(releasesDir, "0.10.10", "bin", "codex")
	if found != expected {
		t.Fatalf("expected %s, got %s", expected, found)
	}
}
