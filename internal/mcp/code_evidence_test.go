package mcp

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/auth"
)

// The three code evidence tools carry an explicit read capability in both
// editions; none falls through to CapAll.
func TestCodeEvidenceToolCapabilities(t *testing.T) {
	for _, name := range []string{"code_context", "code_expand", "code_validate"} {
		if got := requiredCapabilityForTool(name); got != auth.CapTaskRead {
			t.Errorf("%s requires %s, want %s", name, got, auth.CapTaskRead)
		}
	}
}
