//go:build !governed

package tui

import (
	"testing"
)

func TestMarshalSettingsFailoverOrderAcceptsWords(t *testing.T) {
	args := []string{"failover-order", "claude", "->", "codex"}
	if got := joinFailoverArgs(args); len(got) != 2 || got[1] != "claude -> codex" {
		t.Fatalf("joined args = %q", got)
	}
	if got := joinFailoverArgs([]string{"global-cap", "4", "x"}); len(got) != 3 {
		t.Fatalf("other keys must keep their args: %q", got)
	}
}
