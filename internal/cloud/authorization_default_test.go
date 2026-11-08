//go:build governed

package cloud

import "testing"

func TestDefaultAuthorizationStillNeedsCloud(t *testing.T) {
	for _, endpoint := range []string{"", "invalid endpoint"} {
		a := Authorize(t.Context(), Config{Endpoint: endpoint}, t.TempDir(), "test")
		if a.Gate.Entitled() || a.Gate.Capability("ultra.marshal") {
			t.Fatal("default authorization granted ULTRA without the cloud")
		}
		if endpoint != "" && a.Err == nil {
			t.Fatal("invalid cloud endpoint was ignored")
		}
	}
}
