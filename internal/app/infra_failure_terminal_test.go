package app

import (
	"strings"
	"testing"
)

// A tmux query that ran past its deadline on a slow host failed a worker
// attempt as a Codex network problem and cooled Codex down. It is Marshal's
// own terminal, never the provider.
func TestSlowTerminalHostIsNotAProviderFailure(t *testing.T) {
	text := "worker failed: run failed: one or more tasks failed: unavailable: probe Codex exec failed: tmux [list-keys -T root]: signal: killed\ncontext deadline exceeded"
	kind, _ := classifyInfraFailure(text)
	if kind != terminalHostSlow {
		t.Fatalf("slow tmux classified as %q", kind)
	}
	if infraKindCoolsProvider(kind) {
		t.Fatal("a slow terminal cooled the provider down")
	}
	if got := infraFailurePhrase("Codex", kind); strings.Contains(got, "crash") || strings.Contains(got, "network") {
		t.Fatalf("phrase blames the provider: %q", got)
	}
}
