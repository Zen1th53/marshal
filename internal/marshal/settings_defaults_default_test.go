//go:build governed

package marshal

import "testing"

func TestCommunityDefaultAuthority(t *testing.T) {
	s := DefaultSettings()
	if s.ExecutionRights != RightsReadOnly || s.AcceptanceMode != AcceptMarshalThenUser || s.UltraConcurrency != 3 || s.ReworkLimit != 2 || s.Budget != (Budget{}) {
		t.Fatalf("community defaults changed: %+v", s)
	}
}
