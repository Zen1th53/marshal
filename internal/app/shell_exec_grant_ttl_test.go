package app

import (
	"testing"
	"time"
)

// The required project context receipt of a launch ends at its shell-exec
// grant's expiry, so the grant must cover the whole launch: a grant shorter
// than the launch stopped the worker with REQUIRED_CONTEXT_CHANGED.
func TestShellExecGrantCoversTheLaunch(t *testing.T) {
	ttl, launch := buildShellExecGrantTTL(), buildTaskLaunchTimeout()
	if ttl <= 0 {
		t.Fatalf("shell-exec grant must expire; ttl=%v", ttl)
	}
	if launch > 0 && ttl < launch {
		t.Fatalf("grant %v ends before the launch bound %v", ttl, launch)
	}
	if launch == 0 && ttl <= 30*time.Minute {
		t.Fatalf("an unbounded launch keeps a %v grant; long model turns are cut", ttl)
	}
	if ttl > 24*time.Hour {
		t.Fatalf("grant %v is not a bounded lease", ttl)
	}
}
