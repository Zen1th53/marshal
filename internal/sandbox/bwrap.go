package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

type Bwrap struct {
	binary string
}

func NewBwrap(binary string) *Bwrap {
	return &Bwrap{binary: binary}
}

func (b *Bwrap) Probe(ctx context.Context) model.IsolationCapability {
	if runtime.GOOS != "linux" {
		return model.IsolationCapability{Level: model.IsolationProcessOnly, Reason: "bubblewrap is supported only on Linux"}
	}
	if b.binary == "" {
		return model.IsolationCapability{Level: model.IsolationProcessOnly, Reason: "bubblewrap path is empty"}
	}
	if _, err := os.Stat(b.binary); err != nil {
		return model.IsolationCapability{Level: model.IsolationProcessOnly, Reason: "bubblewrap binary is unavailable"}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	args := namespaceArgs()
	for _, path := range systemPaths() {
		args = append(args, "--ro-bind", path, path)
	}
	args = append(args, "--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp", "--", "/usr/bin/true")
	cmd := exec.CommandContext(probeCtx, b.binary, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return model.IsolationCapability{
			Level:  model.IsolationProcessOnly,
			Reason: fmt.Sprintf("bubblewrap namespace probe failed: %v: %s", err, bounded(output, 256)),
		}
	}
	return model.IsolationCapability{
		Level: model.IsolationBwrap, Available: true, Filesystem: true, Process: true,
		Network: true, Reason: "bubblewrap namespace probe passed",
	}
}

func (b *Bwrap) Wrap(request model.SandboxRequest, command []string) (model.CommandSpec, error) {
	if len(command) == 0 {
		return model.CommandSpec{}, fmt.Errorf("%w: sandbox command is empty", model.ErrInvalid)
	}
	worktree, err := existingDirectory(request.Worktree)
	if err != nil {
		return model.CommandSpec{}, fmt.Errorf("%w: worktree: %v", model.ErrInvalid, err)
	}
	args := namespaceArgs()
	if !request.NetworkAllowed {
		args = append(args, "--unshare-net")
	}
	for _, path := range systemPaths() {
		args = append(args, "--ro-bind", path, path)
	}
	args = append(args,
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
		"--dir", "/home",
		"--dir", "/home/marshal",
		"--setenv", "HOME", "/home/marshal",
		"--setenv", "PATH", "/usr/bin:/bin",
		"--bind", worktree, worktree,
	)
	for _, path := range request.WritableDirs {
		resolved, err := existingDirectory(path)
		if err != nil {
			return model.CommandSpec{}, fmt.Errorf("%w: writable bind %s: %v", model.ErrInvalid, path, err)
		}
		args = append(args, "--bind", resolved, resolved)
	}
	for _, mountPath := range request.WritableTmpfs {
		if !filepath.IsAbs(mountPath) {
			return model.CommandSpec{}, fmt.Errorf("%w: writable tmpfs path must be absolute: %s", model.ErrInvalid, mountPath)
		}
		args = append(args, "--tmpfs", mountPath)
	}
	for _, bind := range request.ReadOnlyBinds {
		source, err := existingPath(bind.Source)
		if err != nil || !filepath.IsAbs(bind.Target) ||
			(!pathWithin("/home/marshal", bind.Target) && source != bind.Target) {
			return model.CommandSpec{}, fmt.Errorf("%w: invalid read-only bind %s -> %s", model.ErrInvalid, bind.Source, bind.Target)
		}
		info, err := os.Stat(source)
		if err != nil {
			return model.CommandSpec{}, fmt.Errorf("%w: inspect read-only bind %s: %v", model.ErrInvalid, bind.Source, err)
		}
		targetParent := filepath.Dir(bind.Target)
		if info.IsDir() {
			targetParent = bind.Target
		}
		args = append(args, "--dir", targetParent)
		args = append(args, "--ro-bind", source, bind.Target)
	}
	args = append(args, "--chdir", worktree, "--")
	args = append(args, command...)
	baseEnv := []string{"HOME=/home/marshal", "PATH=/usr/bin:/bin"}
	for _, kv := range request.ExtraEnv {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 {
			args = append([]string{}, args...)
			// insert --setenv before --
			// rebuild args with setenv entries before --chdir
		}
		baseEnv = append(baseEnv, kv)
	}
	// rebuild so --setenv comes before command separator
	var finalArgs []string
	for i, a := range args {
		if a == "--chdir" {
			for _, kv := range request.ExtraEnv {
				parts := strings.SplitN(kv, "=", 2)
				if len(parts) == 2 {
					finalArgs = append(finalArgs, "--setenv", parts[0], parts[1])
				}
			}
			finalArgs = append(finalArgs, args[i:]...)
			break
		}
		finalArgs = append(finalArgs, a)
	}
	if finalArgs == nil {
		finalArgs = args
	}
	return model.CommandSpec{
		Path: b.binary,
		Args: finalArgs,
		Env:  baseEnv,
		Dir:  worktree,
		Isolation: model.IsolationCapability{
			Level: model.IsolationBwrap, Available: true, Filesystem: true, Process: true,
			Network: request.NetworkAllowed, Reason: "bubblewrap command envelope",
		},
	}, nil
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && relative != "." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func ChooseIsolation(capability model.IsolationCapability, risk model.Risk, networkAllowed bool) (model.IsolationCapability, error) {
	if capability.Available && capability.Level == model.IsolationBwrap {
		capability.Network = networkAllowed
		return capability, nil
	}
	if (risk == model.R0 || risk == model.R1) && networkAllowed {
		return model.IsolationCapability{
			Level: model.IsolationProcessOnly, Available: true, Process: true, Network: true,
			Reason: "bubblewrap unavailable; explicit low-risk process-only fallback",
		}, nil
	}
	blocked := model.IsolationCapability{
		Level: model.IsolationBlocked, Available: false, Network: networkAllowed,
		Reason: "required isolation cannot be enforced",
	}
	return blocked, fmt.Errorf("%w: %s", model.ErrUnavailable, blocked.Reason)
}

func namespaceArgs() []string {
	return []string{
		"--die-with-parent",
		"--new-session",
		"--unshare-user",
		"--unshare-pid",
		"--unshare-ipc",
		"--unshare-uts",
	}
}

func systemPaths() []string {
	candidates := []string{"/usr", "/bin", "/lib", "/lib64", "/etc"}
	paths := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			paths = append(paths, candidate)
		}
	}
	return paths
}

func existingDirectory(path string) (string, error) {
	resolved, err := existingPath(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("not a directory")
	}
	return resolved, nil
}

func existingPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}

func bounded(value []byte, limit int) string {
	if len(value) > limit {
		value = value[:limit]
	}
	return string(value)
}
