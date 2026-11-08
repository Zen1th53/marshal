//go:build governed || !linux

package app

import (
	"strings"
	"testing"
)

func TestAGYRemainsNativeWithoutPrivateLinuxGovernance(t *testing.T) {
	r := &Runtime{}
	if _, err := r.VerifyMarshalWorker(t.Context(), "agy", "gemini-3.8-flash-high"); err == nil || !strings.Contains(err.Error(), "supports codex") {
		t.Fatalf("native-only build minted private qualification: %v", err)
	}
	if credentialProvider("agy") {
		t.Fatal("private AGY grant exposed in unsupported build")
	}
	if _, err := providerEndpoint("agy", "gemini-3.8-flash-high"); err == nil {
		t.Fatal("private endpoint exposed in unsupported build")
	}
}
