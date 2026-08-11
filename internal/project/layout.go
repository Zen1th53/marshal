package project

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type Layout struct {
	Root       string
	Branch     string
	HEAD       string
	RuntimeDir string
	Database   string
	Socket     string
	PID        string
	Artifacts  string
	Worktrees  string
	Logs       string
}

func Discover(start string) (Layout, error) {
	root, err := gitOutput(start, "rev-parse", "--show-toplevel")
	if err != nil {
		return Layout{}, fmt.Errorf("discover repository root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return Layout{}, fmt.Errorf("resolve repository root: %w", err)
	}
	branch, err := gitOutput(root, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return Layout{}, fmt.Errorf("discover branch: %w", err)
	}
	head, err := gitOutput(root, "rev-parse", "HEAD")
	if err != nil {
		return Layout{}, fmt.Errorf("discover HEAD: %w", err)
	}
	runtimeDir := filepath.Join(root, ".slaves")
	return Layout{
		Root:       root,
		Branch:     branch,
		HEAD:       head,
		RuntimeDir: runtimeDir,
		Database:   filepath.Join(runtimeDir, "state.db"),
		Socket:     filepath.Join(runtimeDir, "runtime.sock"),
		PID:        filepath.Join(runtimeDir, "pid"),
		Artifacts:  filepath.Join(runtimeDir, "artifacts"),
		Worktrees:  filepath.Join(runtimeDir, "worktrees"),
		Logs:       filepath.Join(runtimeDir, "logs"),
	}, nil
}

func (l Layout) Ensure() error {
	for _, path := range []string{l.RuntimeDir, l.Artifacts, l.Worktrees, l.Logs} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("create runtime directory %s: %w", path, err)
		}
		if err := os.Chmod(path, 0o700); err != nil {
			return fmt.Errorf("secure runtime directory %s: %w", path, err)
		}
	}
	return nil
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %v: %w: %s", args, err, bytes.TrimSpace(stderr.Bytes()))
	}
	return string(bytes.TrimSpace(output)), nil
}
