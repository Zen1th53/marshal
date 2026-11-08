//go:build governed

package app

import (
	"slices"
	"testing"
)

func TestDefaultOpenCodeMarshalRefused(t *testing.T) {
	if slices.Contains(MarshalProviders(), "opencode") {
		t.Fatal("OpenCode eligible")
	}
	if err := (&MarshalCLI{Provider: "opencode"}).turn(t.Context(), "review", "{}", new(any)); err == nil {
		t.Fatal("OpenCode structured turn accepted")
	}
}
