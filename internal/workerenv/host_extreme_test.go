//go:build !governed

package workerenv

import (
	"slices"
	"testing"
)

// In the default edition a host worker's inherited environment is filtered,
// including the process's own when the caller passes nil.
func TestHostFiltersInDefaultEdition(t *testing.T) {
	if !HostFiltered {
		t.Fatal("the default edition must filter host worker environments")
	}
	got := keys(Host(plantedEnv))
	if slices.Contains(got, "GITHUB_TOKEN") || slices.Contains(got, "TMUX") || !slices.Contains(got, "PATH") || !slices.Contains(got, "ANTHROPIC_API_KEY") {
		t.Fatalf("Host(env) = %v", got)
	}
	t.Setenv("GITHUB_TOKEN", "ghp_planted")
	t.Setenv("TMUX", "/private/tmp/tmux-501/default,1,0")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-planted")
	got = keys(Host(nil))
	if slices.Contains(got, "GITHUB_TOKEN") || slices.Contains(got, "TMUX") {
		t.Fatalf("Host(nil) kept a planted variable: %v", got)
	}
	if !slices.Contains(got, "ANTHROPIC_API_KEY") || !slices.Contains(got, "HOME") {
		t.Fatalf("Host(nil) dropped what the worker needs: %v", got)
	}
}
