//go:build !governed

package app

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// The canonical verifier used to bypass VerifierProvider and prefer Claude.
// With only the worker installed it must stop at selection, before touching
// owner context, integration sources, or a provider process.
func TestRequiredVerifierUsesIndependentProviderSelection(t *testing.T) {
	r, s := verifierProviderFixture(t, "claude")
	_, _, err := r.verifyRequiredMarshalIntegration(t.Context(), s, "run", marshal.Run{Tasks: []marshal.Task{{Worker: "claude"}}}, "head")
	if err == nil || !strings.Contains(err.Error(), "codex") || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("canonical verifier bypassed selection: %v", err)
	}
}
