package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// agy verified earlier, then answered a reviewer turn with "Individual quota
// reached ... RESOURCE_EXHAUSTED 429". The private edition moves the role to
// another provider; Community keeps its choice and returns the error.
func TestRetryRoleAfterProviderLimit(t *testing.T) {
	r := openNetpolRuntime(t)
	// Role turns use only CLIs that can host the Marshal MCP server; give the
	// limited provider one such alternative.
	bin := t.TempDir()
	for _, name := range []string{"agy", "claude"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	quota := errors.New("Marshal model turn: exit status 1: Individual quota reached. Resets in 103h (RESOURCE_EXHAUSTED 429)")
	if r.retryRoleAfterLimit(t.Context(), "cross-review", "agy", errors.New("exit status 1: invalid JSON"), nil) {
		t.Fatal("an ordinary failure moved the role")
	}
	moved := r.retryRoleAfterLimit(t.Context(), "cross-review", "agy", quota, []string{"claude"})
	if moved != providerLimitFallback {
		t.Fatalf("moved = %v, want %v for this edition", moved, providerLimitFallback)
	}
	if !providerLimitFallback {
		return
	}
	if reason, limited := r.providerLimited("agy"); !limited || reason == "" {
		t.Fatalf("limit not recorded: %q %v", reason, limited)
	}
	for _, provider := range r.roleProviders() {
		if provider == "agy" {
			t.Fatal("limited provider still offered for a role")
		}
	}
	if r.retryRoleAfterLimit(t.Context(), "cross-review", "agy", quota, []string{"claude", "agy"}) {
		t.Fatal("the same provider was retried twice")
	}
	r.clearProviderLimit("agy")
	if _, limited := r.providerLimited("agy"); limited {
		t.Fatal("successful verification did not clear the limit")
	}
}
