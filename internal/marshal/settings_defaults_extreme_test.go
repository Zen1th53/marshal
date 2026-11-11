//go:build !governed

package marshal

import "testing"

func TestExtremeDefaultAuthority(t *testing.T) {
	s := DefaultSettings()
	if s.ExecutionRights != RightsReadOnly || s.AcceptanceMode != AcceptMarshal || s.UltraConcurrency != 4 || s.Validate() != nil {
		t.Fatalf("private defaults: %+v", s)
	}
}
