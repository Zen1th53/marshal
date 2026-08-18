package router

import "testing"

func TestModelProfileStruct(t *testing.T) {
	mp := ModelProfile{Provider: "claude", Model: "claude-3-5-sonnet", MaxContext: 200000}
	if mp.Provider != "claude" {
		t.Fatalf("expected claude, got %s", mp.Provider)
	}
}
