package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/hostgit"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/sandbox"
)

// RunVerification never falls back to executing project code on the host.
func RunVerification(ctx context.Context, dir string, command []string, timeout time.Duration, limit int, outputDir ...string) (adapter.ProcessResult, error) {
	backend := sandbox.NewBwrap(verificationBwrap())
	return runVerification(ctx, backend, dir, command, timeout, limit, outputDir...)
}

func verificationBwrap() string {
	for _, path := range []string{"/usr/bin/bwrap", "/bin/bwrap"} {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0022 == 0 {
			return path
		}
	}
	return ""
}

func runVerification(ctx context.Context, backend *sandbox.Bwrap, dir string, command []string, timeout time.Duration, limit int, outputDir ...string) (adapter.ProcessResult, error) {
	if _, err := sandbox.ChooseIsolation(backend.Probe(ctx), model.R2, false, false); err != nil {
		return adapter.ProcessResult{}, err
	}
	if len(command) == 0 {
		return adapter.ProcessResult{}, fmt.Errorf("%w: empty verification command", model.ErrInvalid)
	}
	request, err := VerificationSandboxRequest(ctx, dir, outputDir...)
	if err != nil {
		return adapter.ProcessResult{}, err
	}
	result, err := NewObservedSandboxed(New(timeout, 3*time.Second, limit), backend, request, func(_ context.Context, refusal sandbox.Refusal) error {
		return fmt.Errorf("verification socket refused: %+v", refusal)
	}).Run(ctx, adapter.Command{Path: command[0], Args: command[1:], Dir: dir})
	if err == nil {
		err = VerificationResultError(result)
	}
	return result, err
}

// VerificationSandboxRequest preserves read-only metadata and dependency inputs
// for both local verification and run-bound governed checks.
func VerificationSandboxRequest(ctx context.Context, dir string, outputDir ...string) (model.SandboxRequest, error) {
	request := model.SandboxRequest{ReadOnlyWorktree: true, Worktree: dir, ExtraEnv: []string{"GOPROXY=off", "GOSUMDB=off", "GOCACHE=/tmp/go-build", "GOMODCACHE=/home/marshal/go/pkg/mod", "GOTOOLCHAIN=local", "MARSHAL_BUILD_DIR=/tmp/marshal-build", "TMPDIR=/tmp"}}
	request.WritableTmpfs = []string{"/tmp/marshal-build"}
	if len(outputDir) > 0 && outputDir[0] != "" {
		request.WritableDirs = []string{outputDir[0]}
		request.ExtraEnv = append(request.ExtraEnv, "MARSHAL_BUILD_DIR="+outputDir[0])
	}
	// Dependencies are read-only inputs; build caches stay inside the sandbox.
	cache := os.Getenv("GOMODCACHE")
	if cache == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			cache = filepath.Join(home, "go", "pkg", "mod")
		}
	}
	if info, err := os.Stat(cache); err == nil && info.IsDir() {
		request.ReadOnlyBinds = append(request.ReadOnlyBinds, model.Bind{Source: cache, Target: "/home/marshal/go/pkg/mod"})
	}
	// Worktree pointers need their metadata, but checks must not alter it.
	for _, arg := range []string{"--absolute-git-dir", "--git-common-dir"} {
		cmd, err := hostgit.Command(ctx, dir, "rev-parse", arg)
		if err != nil {
			return model.SandboxRequest{}, err
		}
		if out, err := cmd.Output(); err == nil {
			path := strings.TrimSpace(string(out))
			if !filepath.IsAbs(path) {
				path = filepath.Join(dir, path)
			}
			path, err = filepath.EvalSymlinks(path)
			if err != nil {
				return model.SandboxRequest{}, err
			}
			request.ReadOnlyBinds = append(request.ReadOnlyBinds, model.Bind{Source: path, Target: path})
		}
	}
	return request, nil
}

// VerificationResultError prevents partial evidence from becoming a passing check.
func VerificationResultError(result adapter.ProcessResult) error {
	if result.Isolation.Level != model.IsolationBwrap || !result.Isolation.Available || result.TimedOut || result.Cancelled || result.OutputTruncated {
		return fmt.Errorf("%w: verification did not complete confined", model.ErrUnavailable)
	}
	return nil
}
