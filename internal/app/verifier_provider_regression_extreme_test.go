//go:build !governed

package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// The canonical verifier used to bypass VerifierProvider and prefer Claude.
// With no provider able to check installed it must stop at selection, before
// touching owner context, integration sources, or a provider process. (A
// worker's provider may check in a fresh session, so the worker alone being
// installed no longer stops it here.)
func TestRequiredVerifierUsesIndependentProviderSelection(t *testing.T) {
	r, s := verifierProviderFixture(t)
	_, _, err := r.verifyRequiredMarshalIntegration(t.Context(), s, "run", marshal.Run{Tasks: []marshal.Task{{Worker: "claude"}}}, "head")
	if !errors.Is(err, errIndependentVerifierUnavailable) || !strings.Contains(err.Error(), "claude: not installed") {
		t.Fatalf("canonical verifier bypassed selection: %v", err)
	}
}
