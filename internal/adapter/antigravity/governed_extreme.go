//go:build !governed && unix

package antigravity

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/sessionmcp"
)

// NewGoverned uses only the Runtime's bound worker MCP authority and confinement.
func NewGoverned(binary string, runner adapter.ProcessRunner) *Client {
	c := New(binary, runner)
	c.governed = true
	return c
}

// NewGovernedProbe proves headless transport without a worker principal or tools.
func NewGovernedProbe(binary string, runner adapter.ProcessRunner) *Client {
	c := NewGoverned(binary, runner)
	c.probeOnly = true
	return c
}

func nativeIdentifier(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for i, ch := range []byte(s) {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' {
			continue
		}
		if i > 0 && (ch == '-' || ch == '_' || ch == '.' || ch == ':' || ch == '/') {
			continue
		}
		return false
	}
	return true
}

func (c *Client) prepareInvocation(ctx context.Context, req adapter.Request, args []string, session string) (context.Context, []string, []string, func(), error) {
	noop := func() {}
	if !c.governed {
		return ctx, args, nil, noop, nil
	}
	if !nativeIdentifier(req.Model) || session != "" && !nativeIdentifier(session) {
		return ctx, nil, nil, noop, fmt.Errorf("%w: invalid governed Antigravity model/session selector", model.ErrInvalid)
	}
	// In Extreme, an unverified newer version must not block work. It runs
	// the same governed invocation as a qualified one (accept-edits with the
	// bound worker MCP) and its evidence is flagged unverified_version. It
	// used to keep read-only plan mode, so every edit and command was denied
	// and governed work on a newer agy always ended blocked.
	if !c.unverifiedNewer && !isQualifiedGovernedVersion(c.version) {
		return ctx, nil, nil, noop, fmt.Errorf("%w: governed Antigravity requires a qualified probe (%s)", model.ErrUnavailable, GovernedCLIVersionRange)
	}
	if c.probeOnly {
		if session != "" {
			return ctx, nil, nil, noop, fmt.Errorf("%w: Antigravity probe cannot resume a conversation", model.ErrInvalid)
		}
		prepared, cleanup, err := sessionmcp.AntigravityProbeContext(ctx, req.Worktree, req.TaskID)
		return prepared, args, nil, cleanup, err
	}
	args = append([]string(nil), args...)
	args[1] = "accept-edits"
	args, env, cleanup, err := sessionmcp.ConfigureBoundAntigravity(ctx, args, req.TaskID, req.Worktree)
	return ctx, args, env, cleanup, err
}

func (c *Client) validateGovernedVersion(version string) error {
	if !c.governed {
		return nil
	}
	c.unverifiedNewer = false
	if isQualifiedGovernedVersion(version) {
		return nil
	}
	if isUnverifiedNewerVersion(version) {
		// In Extreme, an unverified newer version must not block work:
		// mark to proceed natively with evidence.
		c.unverifiedNewer = true
		return nil
	}
	observed := "UNKNOWN"
	if safeObservedVersion.MatchString(version) {
		observed = version
	}
	return fmt.Errorf("%w: governed Antigravity CLI version mismatch (observed=%s; qualified=%s); this installation requires separate OAuth/configuration and transport qualification before governed use", model.ErrUnavailable, observed, GovernedCLIVersionRange)
}
