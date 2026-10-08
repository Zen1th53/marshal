package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/netpolicy"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/sandbox"
)

// HasCredentialGrant replays committed project operator decisions. Failure to
// read evidence is denial; grants are never inferred from messages or config.
func (r *Runtime) HasCredentialGrant(ctx context.Context, provider string) bool {
	if r == nil || r.store == nil {
		return false
	}
	records, err := r.store.ListEvents(ctx)
	if err != nil {
		return false
	}
	allowed := false
	for _, record := range records {
		if record.Type == "PERMISSION_DECIDED" && record.ProjectID == r.ProjectID() && record.Data["kind"] == "credential" && record.Data["object"] == provider {
			allowed, _ = record.Data["allow"].(bool)
		}
	}
	return allowed
}

func credentialProvider(provider string) bool {
	switch provider {
	case "codex", "claude", "gemini", "opencode":
		return true
	}
	return false
}

func (r *Runtime) providerBroker(ctx context.Context, provider, model string) (*netpolicy.CredentialBroker, error) {
	// OpenCode's free/local providers do not require operator credentials.
	if provider == "opencode" && !netpolicy.CredentialProfileSupported(provider, model) {
		return nil, nil
	}
	granted, err := r.waitCredentialGrant(ctx, provider, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("%w: %s credential permission unavailable: %w", netpolicy.ErrCredentialBroker, provider, err)
	}
	if !granted {
		return nil, fmt.Errorf("%w: %s credential use denied (operator denial or permission timeout)", netpolicy.ErrCredentialBroker, provider)
	}
	return netpolicy.LoadCredentialBroker(provider, model)
}

func (r *Runtime) sandboxBroker(socket, home string) ([]string, error) {
	r.egressMu.Lock()
	var broker *netpolicy.CredentialBroker
	var provider string
	for _, scope := range r.egressRuns {
		if scope.socket == socket {
			broker = scope.broker
			provider = scope.provider
			break
		}
	}
	r.egressMu.Unlock()
	if broker == nil {
		return nil, nil
	}
	if !r.HasCredentialGrant(context.Background(), provider) {
		return nil, netpolicy.ErrCredentialBroker
	}
	roots, err := sandbox.SystemRootBundle()
	if err != nil {
		return nil, err
	}
	return broker.Prepare(home, "/home/marshal", roots)
}

// Wait only when a decision surface exists. Persistent denial remains denial;
// an undecided request cannot fail a governed run ahead of its operator popup.
func (r *Runtime) waitCredentialGrant(ctx context.Context, provider string, timeout time.Duration) (bool, error) {
	decision := func() (bool, bool, error) {
		records, err := r.store.ListEvents(ctx)
		if err != nil {
			return false, false, err
		}
		allowed, decided := false, false
		for _, e := range records {
			if e.Type == "PERMISSION_DECIDED" && e.ProjectID == r.ProjectID() && e.Data["kind"] == "credential" && e.Data["object"] == provider {
				allowed, _ = e.Data["allow"].(bool)
				decided = true
			}
		}
		return allowed, decided, nil
	}
	if allowed, decided, err := decision(); err != nil || decided {
		return allowed, err
	}
	r.permissionMu.Lock()
	sink := r.permissionSink
	r.permissionMu.Unlock()
	if sink == nil {
		return false, errors.New("no operator decision surface")
	}
	sink(permission.Request{Kind: "credential", Object: provider, Scope: "this project, until revoked", Who: "MARSHAL"})
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if allowed, decided, err := decision(); err != nil || decided {
			return allowed, err
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-timer.C:
			return false, nil
		case <-ticker.C:
		}
	}
}
