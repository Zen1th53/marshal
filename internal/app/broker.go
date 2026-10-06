package app

import (
	"context"
	"fmt"

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
	if !r.HasCredentialGrant(ctx, provider) {
		r.permissionMu.Lock()
		sink := r.permissionSink
		r.permissionMu.Unlock()
		if sink != nil {
			sink(permission.Request{Kind: "credential", Object: provider, Scope: "this project, until revoked", Who: "MARSHAL"})
		}
		return nil, fmt.Errorf("%w: %s credential use denied; allow the credential broker permission prompt and retry", netpolicy.ErrCredentialBroker, provider)
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
