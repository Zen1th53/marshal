package worker

import (
	"context"
	"errors"
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

// RunVerification uses host execution in Extreme. Community uses confinement
// and permits host fallback only when its edition policy explicitly allows it.
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
	if sandbox.HostExecutionPreferred() {
		return runUnconfined(ctx, errHostVerification, dir, command, timeout, limit, outputDir...)
	}
	if _, err := sandbox.ChooseIsolation(backend.Probe(ctx), model.R2, false, false); err != nil {
		if !sandbox.UnconfinedFallbackAllowed() {
			return adapter.ProcessResult{}, err
		}
		return runUnconfined(ctx, err, dir, command, timeout, limit, outputDir...)
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
	if err != nil && sandbox.UnconfinedFallbackAllowed() && !result.TimedOut && !result.Cancelled && !result.OutputTruncated {
		return runUnconfined(ctx, err, dir, command, timeout, limit, outputDir...)
	}
	return result, err
}

// errHostVerification is the recorded cause when the Marshal build verifies
// on the host by design.
var errHostVerification = errors.New("Marshal verifies on the host by design")

// UnconfinedReasonPrefix marks verification evidence produced on the host
// without confinement (Marshal build only).
const UnconfinedReasonPrefix = "unconfined: "

func runUnconfined(ctx context.Context, cause error, dir string, command []string, timeout time.Duration, limit int, outputDir ...string) (adapter.ProcessResult, error) {
	if len(command) == 0 {
		return adapter.ProcessResult{}, fmt.Errorf("%w: empty verification command", model.ErrInvalid)
	}
	// The host run keeps the host environment and, like the sandbox, gives the
	// command its build output directory: the caller's when supplied, otherwise
	// a per-user cache directory so builds never fall back to a small or
	// quota-limited /tmp.
	build := DefaultBuildDir()
	if len(outputDir) > 0 && outputDir[0] != "" {
		build = outputDir[0]
	}
	var env []string
	if build != "" {
		env = append(os.Environ(), "MARSHAL_BUILD_DIR="+build)
	}
	result, err := New(timeout, 3*time.Second, limit).Run(ctx, adapter.Command{Path: command[0], Args: command[1:], Dir: dir, Env: env})
	result.Isolation = model.IsolationCapability{Level: model.IsolationProcessOnly, Reason: UnconfinedReasonPrefix + "ran on the host after confinement refused: " + cause.Error()}
	if err == nil && (result.TimedOut || result.Cancelled || result.OutputTruncated) {
		err = fmt.Errorf("%w: verification did not complete", model.ErrUnavailable)
	}
	return result, err
}

// DefaultBuildDir is the build output directory used when a caller supplies
// none. It is a per-user cache directory ($XDG_CACHE_HOME/marshal/build, else
// ~/.cache/marshal/build), created 0700, so host verification never writes to
// a small or quota-limited /tmp. MARSHAL_BUILD_DIR still overrides it in the
// check's own environment. It falls back to os.TempDir only when no home or
// cache directory is available or the directory cannot be created.
func DefaultBuildDir() string {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			base = filepath.Join(home, ".cache")
		}
	}
	if base == "" {
		return os.TempDir()
	}
	dir := filepath.Join(base, "marshal", "build")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return os.TempDir()
	}
	return dir
}

// sandboxGoModCache is where a sandbox sees the host's Go module cache.
const sandboxGoModCache = "/home/marshal/go/pkg/mod"

// GoModuleCacheBind is a read-only bind of the host's Go module cache at
// sandboxGoModCache, or false when the host has none.
func GoModuleCacheBind() (model.Bind, bool) {
	cache := os.Getenv("GOMODCACHE")
	if cache == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			cache = filepath.Join(home, "go", "pkg", "mod")
		}
	}
	if info, err := os.Stat(cache); err == nil && info.IsDir() {
		return model.Bind{Source: cache, Target: sandboxGoModCache}, true
	}
	return model.Bind{}, false
}

// WorkerGoEnv is the Go environment of a sandboxed worker given the module
// cache bind: modules resolve offline from the read-only cache, so a build
// never downloads dependencies, and build caches stay in the worker's home.
func WorkerGoEnv() []string {
	return []string{"GOMODCACHE=" + sandboxGoModCache, "GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local"}
}

// VerificationSandboxRequest preserves read-only metadata and dependency inputs
// for both local verification and run-bound governed checks.
func VerificationSandboxRequest(ctx context.Context, dir string, outputDir ...string) (model.SandboxRequest, error) {
	request := model.SandboxRequest{ReadOnlyWorktree: true, Worktree: dir, ExtraEnv: []string{"GOPROXY=off", "GOSUMDB=off", "GOCACHE=/tmp/go-build", "GOMODCACHE=" + sandboxGoModCache, "GOTOOLCHAIN=local", "MARSHAL_BUILD_DIR=/tmp/marshal-build", "TMPDIR=/tmp"}}
	request.WritableTmpfs = []string{"/tmp/marshal-build"}
	if len(outputDir) > 0 && outputDir[0] != "" {
		request.WritableDirs = []string{outputDir[0]}
		request.ExtraEnv = append(request.ExtraEnv, "MARSHAL_BUILD_DIR="+outputDir[0])
	}
	// Dependencies are read-only inputs; build caches stay inside the sandbox.
	if bind, ok := GoModuleCacheBind(); ok {
		request.ReadOnlyBinds = append(request.ReadOnlyBinds, bind)
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
	if sandbox.UnconfinedFallbackAllowed() && strings.HasPrefix(result.Isolation.Reason, UnconfinedReasonPrefix) && !result.TimedOut && !result.Cancelled && !result.OutputTruncated {
		return nil
	}
	if result.Isolation.Level != model.IsolationBwrap || !result.Isolation.Available || result.TimedOut || result.Cancelled || result.OutputTruncated {
		return fmt.Errorf("%w: verification did not complete confined", model.ErrUnavailable)
	}
	return nil
}
