//go:build !governed

package startup_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/startup"
)

// TestShippedUltraReadySummaryIsPlain: in the shipped build ULTRA runs
// locally, so `marshal setup` says what it lets the user do, not the bare
// product word "ULTRA is available."
func TestShippedUltraReadySummaryIsPlain(t *testing.T) {
	root := "/work/project"
	env := healthyEnv(root)
	env.UltraEntitled = true
	check, ok := startup.Assess(context.Background(), healthyProject(root), env).Check("env.ultra")
	if !ok {
		t.Fatal("env.ultra check missing")
	}
	if check.Summary == "ULTRA is available." || !strings.Contains(check.Summary, "several tasks at once") {
		t.Fatalf("summary = %q, want what ULTRA means for the user", check.Summary)
	}
}
