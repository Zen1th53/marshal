package execution

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestProvenance_InspectBinary(t *testing.T) {
	prov, err := InspectBinaryProvenance("git")
	if err != nil {
		t.Fatalf("InspectBinaryProvenance failed: %v", err)
	}

	if prov.ResolvedPath == "" {
		t.Errorf("expected non-empty ResolvedPath")
	}
	if prov.SHA256 == "" {
		t.Errorf("expected non-empty SHA256 digest")
	}

	// Non-existent binary
	_, err = InspectBinaryProvenance("non_existent_binary_xyz_12345")
	if err == nil {
		t.Errorf("expected error for non-existent binary, got nil")
	}
}

func TestProvenance_ValidateAllowedBinary_ShadowingProtection(t *testing.T) {
	// Valid system binary
	if err := ValidateAllowedBinary("/usr/bin/git", nil); err != nil {
		t.Errorf("expected /usr/bin/git to be allowed: %v", err)
	}

	// Blocked: /tmp execution
	err := ValidateAllowedBinary("/tmp/malicious_git", nil)
	if err == nil {
		t.Errorf("expected error for /tmp binary, got nil")
	}
	if !errors.Is(err, ErrUnauthorizedWorker) {
		t.Errorf("expected ErrUnauthorizedWorker, got: %v", err)
	}

	// Blocked: ./ local directory shadowing
	err = ValidateAllowedBinary("./git", nil)
	if err == nil {
		t.Errorf("expected error for relative ./ binary, got nil")
	}
	if !errors.Is(err, ErrUnauthorizedWorker) {
		t.Errorf("expected ErrUnauthorizedWorker, got: %v", err)
	}
}

func TestProvenance_GitEnvironmentIntegrity(t *testing.T) {
	ctx := context.Background()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}

	env, err := InspectGitEnvironment(ctx, cwd)
	if err != nil {
		t.Fatalf("InspectGitEnvironment failed: %v", err)
	}

	if env.RepoRoot == "" {
		t.Errorf("expected non-empty RepoRoot")
	}
	if env.HeadCommit == "" {
		t.Errorf("expected non-empty HeadCommit")
	}
	if env.Branch == "" {
		t.Errorf("expected non-empty Branch")
	}

	// Verify matching environment passes
	if err := VerifyGitEnvironment(env.RepoRoot, env.Branch, env); err != nil {
		t.Errorf("VerifyGitEnvironment failed for valid environment: %v", err)
	}

	// Wrong repo root detected and blocked
	err = VerifyGitEnvironment("/some/other/repo", env.Branch, env)
	if err == nil {
		t.Errorf("expected error for mismatched repo root, got nil")
	}
	if !errors.Is(err, ErrIsolationCompromised) {
		t.Errorf("expected ErrIsolationCompromised, got: %v", err)
	}

	// Wrong branch detected and blocked
	err = VerifyGitEnvironment(env.RepoRoot, "feature-nonexistent-branch", env)
	if err == nil {
		t.Errorf("expected error for mismatched branch, got nil")
	}
	if !errors.Is(err, ErrIsolationCompromised) {
		t.Errorf("expected ErrIsolationCompromised, got: %v", err)
	}
}

func TestProvenance_SymlinkDetection(t *testing.T) {
	tmpDir := t.TempDir()
	origBin := filepath.Join(tmpDir, "real_bin")
	_ = os.WriteFile(origBin, []byte("#!/bin/sh\necho hello"), 0755)

	symlinkBin := filepath.Join(tmpDir, "symlink_bin")
	_ = os.Symlink(origBin, symlinkBin)

	prov, err := InspectBinaryProvenance(symlinkBin)
	if err != nil {
		t.Fatalf("InspectBinaryProvenance failed on symlink: %v", err)
	}

	if !prov.IsSymlink {
		t.Errorf("expected IsSymlink = true")
	}
	if prov.TargetFile != origBin {
		t.Errorf("expected TargetFile %s, got %s", origBin, prov.TargetFile)
	}
}
