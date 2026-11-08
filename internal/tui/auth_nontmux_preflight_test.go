//go:build linux

package tui

import (
	"context"
	"io"
	"sync/atomic"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

// authNontmuxParticipant returns the workspace participant for agentID.
func authNontmuxParticipant(ws *Workspace, agentID string) (model.Participant, bool) {
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	for _, p := range ws.state.Participants {
		if p.AgentID == agentID {
			return p, true
		}
	}
	return model.Participant{}, false
}

// TestAuthPreflightNonTmuxBlocksLaunch proves the direct TTY takeover path runs
// the provider-auth preflight before surrendering the terminal and refuses an
// unsigned-in provider before its binary is launched.
func TestAuthPreflightNonTmuxBlocksLaunch(t *testing.T) {
	sweepWorkEnvironment(t)
	installDialectDoubles(t)
	_, ws, ctx := acceptanceWorkspace(t)
	ws.terminal = &Terminal{isTerm: true, out: io.Discard, inFd: -1, outFd: -1}

	t.Cleanup(SetAuthPreflightForTest(func(ctx context.Context, provider, binary string, env []string) error {
		if canonicalAuthProvider(provider) == "codex" {
			return ErrProviderNotSignedIn
		}
		return nil
	}))

	_, err := ws.runNativeAgent(ctx, "codex", nil)
	if err == nil || err.Error() != "Codex is not signed in - run `codex login`" {
		t.Fatalf("expected non-tmux launch to refuse with auth error, got %v", err)
	}

	p, ok := authNontmuxParticipant(ws, "codex")
	if !ok {
		t.Fatal("codex participant missing after preflight refusal")
	}
	if p.IsActive || p.Model != UnavailableModel {
		t.Fatalf("expected codex UNAVAILABLE after preflight refusal, got IsActive=%v Model=%q", p.IsActive, p.Model)
	}
}

// TestAuthPreflightNonTmuxSkipsLoginSubcommand proves the skip logic is
// mirrored from the tmux path: an explicit login subcommand bypasses the
// preflight so the operator can sign in.
func TestAuthPreflightNonTmuxSkipsLoginSubcommand(t *testing.T) {
	sweepWorkEnvironment(t)
	installDialectDoubles(t)
	_, ws, ctx := acceptanceWorkspace(t)
	ws.terminal = &Terminal{isTerm: true, out: io.Discard, inFd: -1, outFd: -1}

	var preflightCalls atomic.Int32
	t.Cleanup(SetAuthPreflightForTest(func(ctx context.Context, provider, binary string, env []string) error {
		preflightCalls.Add(1)
		return nil
	}))

	if _, err := ws.runNativeAgent(ctx, "codex", []string{"login"}); err != nil {
		t.Fatalf("expected /codex login to bypass preflight, got %v", err)
	}
	if preflightCalls.Load() != 0 {
		t.Fatalf("expected auth preflight to be skipped for login, got %d call(s)", preflightCalls.Load())
	}
}
