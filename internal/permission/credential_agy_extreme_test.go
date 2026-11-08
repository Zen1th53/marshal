//go:build !governed && linux

package permission

import (
	"strings"
	"testing"
)

func TestAGYCredentialPermissionHasSeparateNativeScope(t *testing.T) {
	text, err := Render([]Request{{Kind: "credential", Object: "agy"}})
	if err != nil || !strings.Contains(text, "Antigravity") || !strings.Contains(text, "this project, until revoked") {
		t.Fatalf("native AGY credential decision unavailable: %v %q", err, text)
	}
	for _, alias := range []string{"antigravity", "google-account", "AGY"} {
		if _, err := Render([]Request{{Kind: "credential", Object: alias}}); err == nil {
			t.Fatalf("credential alias %q admitted", alias)
		}
	}
}
