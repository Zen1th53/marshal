package tui

import "testing"

// v verifies the provider that leads first: nothing can be planned before
// the Marshal itself is verified.
func TestVerifyTheLeadFirst(t *testing.T) {
	chips := []providerChip{{name: "codex", state: "unverified"}, {name: "claude", state: "unverified"}, {name: "agy", state: "unverified"}}
	if got := nextUnverified(chips, "agy"); got != "agy" {
		t.Fatalf("lead agy unverified: v verifies %q", got)
	}
	chips[2].state = "verified"
	if got := nextUnverified(chips, "agy"); got != "codex" {
		t.Fatalf("lead verified: v verifies %q, want the first unverified", got)
	}
	if got := nextUnverified(chips, ""); got != "codex" {
		t.Fatalf("no lead yet: v verifies %q", got)
	}
}
