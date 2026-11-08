//go:build governed || !linux

package permission

import "testing"

func TestAGYCredentialPermissionUnsupportedBuildRefuses(t *testing.T) {
	if _, err := Render([]Request{{Kind: "credential", Object: "agy"}}); err == nil {
		t.Fatal("private native AGY grant admitted on unsupported build")
	}
	if _, err := Render([]Request{{Kind: "credential", Object: "gemini"}}); err == nil {
		t.Fatal("retired Gemini credential provider admitted")
	}
}
