package opencode

import (
	"context"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/workerenv"
)

type envCaptureRunner struct{ env []string }

func (r *envCaptureRunner) Run(_ context.Context, command adapter.Command) (adapter.ProcessResult, error) {
	r.env = append([]string(nil), command.Env...)
	return adapter.ProcessResult{ExitCode: 1}, nil
}

// The opencode worker the adapter starts gets the host worker environment: in
// the default edition no planted GITHUB_TOKEN or tmux client variables, but
// PATH, HOME and the provider's auth variable; in the governed edition the
// owner's environment as before.
func TestRunPassesHostWorkerEnv(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_planted")
	t.Setenv("TMUX", "/private/tmp/tmux-501/default,1,0")
	t.Setenv("TMUX_PANE", "%1")
	t.Setenv("OPENCODE_API_KEY", "planted-auth")
	runner := &envCaptureRunner{}
	_, _ = New("opencode", runner).Run(t.Context(), adapter.Request{TaskID: "task", Title: "fix", Worktree: t.TempDir()})
	got := map[string]bool{}
	for _, e := range runner.env {
		k, _, _ := strings.Cut(e, "=")
		got[k] = true
	}
	if !got["PATH"] || !got["HOME"] || !got["OPENCODE_API_KEY"] {
		t.Fatalf("worker lost PATH, HOME or OPENCODE_API_KEY: %d entries", len(runner.env))
	}
	if workerenv.HostFiltered && (got["GITHUB_TOKEN"] || got["TMUX"] || got["TMUX_PANE"]) {
		t.Fatal("host worker inherited GITHUB_TOKEN or the tmux client variables")
	}
	if !workerenv.HostFiltered && !got["GITHUB_TOKEN"] {
		t.Fatal("governed edition environment changed")
	}
}
