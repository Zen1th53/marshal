package optimization

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	marshalmodel "github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/sandbox"
)

// BwrapReplayRunner is the concrete local replay runner. It resolves a pinned
// tree, wraps a route-specific command in bubblewrap, and lets a verifier turn
// process output into an honest observation. Process exit is never treated as
// a verified PASS by this runner.
type BwrapReplayRunner struct {
	// BwrapBinary is the exact bubblewrap executable used for replay.
	BwrapBinary string
	// ResolveTree materializes the immutable tree named by TreeDigest. The
	// returned directory must be beneath SandboxPolicy.WritableRoot.
	ResolveTree func(context.Context, string) (string, error)
	// CommandFor maps a governed alternate route to its executable command.
	// It receives no secret inputs.
	CommandFor func(ReplayRequest) ([]string, error)
	// Verify interprets runner output using the independent verifier policy.
	// A nil verifier is refused: process success is not verification.
	Verify func(context.Context, ReplayRequest, []byte, error) (ReplayObservation, error)
}

func (r BwrapReplayRunner) Replay(ctx context.Context, request ReplayRequest) (ReplayObservation, error) {
	if r.ResolveTree == nil || r.CommandFor == nil || r.Verify == nil {
		return ReplayObservation{}, fmt.Errorf("%w: bwrap replay runner is missing resolver, command, or verifier", ErrInvalid)
	}
	if request.Sandbox.NetworkEnabled || request.Sandbox.ProductionCredentials {
		return ReplayObservation{}, fmt.Errorf("%w: bwrap replay requested network or production credentials", ErrUnsafeCounterfactual)
	}
	worktree, err := r.ResolveTree(ctx, request.TreeDigest)
	if err != nil {
		return ReplayObservation{}, fmt.Errorf("resolve replay tree: %w", err)
	}
	if !pathWithinRoot(request.Sandbox.WritableRoot, worktree) {
		return ReplayObservation{}, fmt.Errorf("%w: resolved replay tree lies outside bounded writable root", ErrUnsafeCounterfactual)
	}
	command, err := r.CommandFor(request)
	if err != nil {
		return ReplayObservation{}, fmt.Errorf("build alternate replay command: %w", err)
	}
	if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
		return ReplayObservation{}, fmt.Errorf("%w: alternate replay command", ErrInvalid)
	}

	wrapped, err := sandbox.NewBwrap(r.BwrapBinary).Wrap(marshalmodel.SandboxRequest{
		Worktree: worktree, NetworkAllowed: false,
		WritableDirs: []string{request.Sandbox.WritableRoot},
	}, command)
	if err != nil {
		return ReplayObservation{}, fmt.Errorf("build offline replay sandbox: %w", err)
	}
	cmd := exec.CommandContext(ctx, wrapped.Path, wrapped.Args...)
	cmd.Dir, cmd.Env = wrapped.Dir, wrapped.Env
	output, runErr := cmd.CombinedOutput()
	return r.Verify(ctx, request, output, runErr)
}

func pathWithinRoot(root, candidate string) bool {
	resolvedRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	resolvedCandidate, err := filepath.Abs(candidate)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedCandidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
