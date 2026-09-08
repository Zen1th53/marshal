package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// BinaryProvenance records supply-chain metadata for an executable tool.
type BinaryProvenance struct {
	ResolvedPath string `json:"resolved_path"`
	Version      string `json:"version,omitempty"`
	SHA256       string `json:"sha256,omitempty"`
	IsSymlink    bool   `json:"is_symlink"`
	TargetFile   string `json:"target_file,omitempty"`
}

// InspectBinaryProvenance inspects an executable on disk and calculates its provenance.
func InspectBinaryProvenance(binNameOrPath string) (BinaryProvenance, error) {
	resolved, err := exec.LookPath(binNameOrPath)
	if err != nil {
		return BinaryProvenance{}, fmt.Errorf("tool %q not found in PATH: %w", binNameOrPath, err)
	}

	cleanPath, err := filepath.Abs(resolved)
	if err != nil {
		cleanPath = filepath.Clean(resolved)
	}

	fi, err := os.Lstat(cleanPath)
	if err != nil {
		return BinaryProvenance{}, fmt.Errorf("failed to stat tool binary %s: %w", cleanPath, err)
	}

	isSymlink := (fi.Mode() & os.ModeSymlink) != 0
	var targetFile string
	if isSymlink {
		target, err := filepath.EvalSymlinks(cleanPath)
		if err == nil {
			targetFile = target
		}
	}

	// Compute binary SHA256
	var hashStr string
	f, err := os.Open(cleanPath)
	if err == nil {
		defer f.Close()
		h := sha256.New()
		if _, err := io.Copy(h, f); err == nil {
			hashStr = hex.EncodeToString(h.Sum(nil))
		}
	}

	return BinaryProvenance{
		ResolvedPath: cleanPath,
		SHA256:       hashStr,
		IsSymlink:    isSymlink,
		TargetFile:   targetFile,
	}, nil
}

// ValidateAllowedBinary verifies that a resolved binary does not execute from untrusted/shadowed directories.
func ValidateAllowedBinary(resolvedPath string, blockedDirs []string) error {
	clean := filepath.Clean(resolvedPath)

	// Block current working directory shadowing or temp directory execution unless explicitly permitted
	if blockedDirs == nil {
		blockedDirs = []string{"/tmp", "/var/tmp", "./", "."}
	}

	for _, b := range blockedDirs {
		if (b == "." || b == "./") && (!filepath.IsAbs(clean) || strings.HasPrefix(resolvedPath, ".")) {
			return fmt.Errorf("%w: binary %s located in untrusted/shadowed directory %s",
				ErrUnauthorizedWorker, clean, b)
		}
		cleanB := filepath.Clean(b)
		if filepath.IsAbs(cleanB) {
			if strings.HasPrefix(clean, cleanB+"/") || clean == cleanB {
				return fmt.Errorf("%w: binary %s located in untrusted/shadowed directory %s",
					ErrUnauthorizedWorker, clean, cleanB)
			}
		}
	}

	return nil
}

// GitEnvironment captures current repository and branch state to enforce integrity.
type GitEnvironment struct {
	RepoRoot    string `json:"repo_root"`
	Branch      string `json:"branch"`
	HeadCommit  string `json:"head_commit"`
	IsDirty     bool   `json:"is_dirty"`
	RemoteURL   string `json:"remote_url,omitempty"`
}

// InspectGitEnvironment inspects the repository status at dir.
func InspectGitEnvironment(ctx context.Context, dir string) (GitEnvironment, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return GitEnvironment{}, err
	}

	rootCmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	rootCmd.Dir = absDir
	rootOut, err := rootCmd.Output()
	if err != nil {
		return GitEnvironment{}, fmt.Errorf("not a git repository: %w", err)
	}
	repoRoot := strings.TrimSpace(string(rootOut))

	branchCmd := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD")
	branchCmd.Dir = repoRoot
	branchOut, _ := branchCmd.Output()
	branch := strings.TrimSpace(string(branchOut))

	headCmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	headCmd.Dir = repoRoot
	headOut, _ := headCmd.Output()
	headCommit := strings.TrimSpace(string(headOut))

	statusCmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
	statusCmd.Dir = repoRoot
	statusOut, _ := statusCmd.Output()
	isDirty := len(strings.TrimSpace(string(statusOut))) > 0

	remoteCmd := exec.CommandContext(ctx, "git", "remote", "get-url", "origin")
	remoteCmd.Dir = repoRoot
	remoteOut, _ := remoteCmd.Output()
	remoteURL := strings.TrimSpace(string(remoteOut))

	return GitEnvironment{
		RepoRoot:   repoRoot,
		Branch:     branch,
		HeadCommit: headCommit,
		IsDirty:    isDirty,
		RemoteURL:  remoteURL,
	}, nil
}

// VerifyGitEnvironment asserts that the execution occurs on expected repo root and branch.
func VerifyGitEnvironment(expectedRoot, expectedBranch string, env GitEnvironment) error {
	if expectedRoot != "" {
		cleanExp, err1 := filepath.Abs(expectedRoot)
		cleanAct, err2 := filepath.Abs(env.RepoRoot)
		if err1 == nil && err2 == nil && cleanExp != cleanAct {
			return fmt.Errorf("%w: repository root mismatch (expected %s, got %s)",
				ErrIsolationCompromised, cleanExp, cleanAct)
		}
	}

	if expectedBranch != "" && env.Branch != expectedBranch && env.Branch != "HEAD" {
		return fmt.Errorf("%w: unexpected branch switch (expected %s, got %s)",
			ErrIsolationCompromised, expectedBranch, env.Branch)
	}

	return nil
}
