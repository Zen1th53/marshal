//go:build linux && !governed

package tui

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestExtremeSessionStartsUltra(t *testing.T) {
	w := NewWorkspace(nil, "proj", "session")
	defer w.Close()
	w.AttachULTRASession(cloud.Authorize(t.Context(), cloud.Config{}, t.TempDir(), "test"))
	policy := marshal.TierPolicy(workspaceUltraGate{workspace: w}, marshal.DefaultSettings())
	if policy.Tier != marshal.Ultra {
		t.Fatalf("startup policy: %+v", policy)
	}
}
