//go:build !governed && unix

package driver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// envDumpCLI writes its environment to the file named by its only argument
// (never by an environment variable, which the filter could drop).
const envDumpCLI = "#!/bin/sh\nenv > \"$1\"\n"

func plantOwnerEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GITHUB_TOKEN", "ghp_planted")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "aws_planted")
	t.Setenv("SSH_AUTH_SOCK", "/private/tmp/agent.sock")
	t.Setenv("TMUX", "/private/tmp/tmux-501/default,1,0")
	t.Setenv("TMUX_PANE", "%1")
	t.Setenv("TMUX_TMPDIR", "/private/tmp")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-planted")
	t.Setenv("HOME", t.TempDir())
}

func checkWorkerEnv(t *testing.T, dump string) {
	t.Helper()
	data, err := os.ReadFile(dump)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		if k, _, ok := strings.Cut(line, "="); ok {
			got[k] = true
		}
	}
	for _, banned := range []string{"GITHUB_TOKEN", "AWS_SECRET_ACCESS_KEY", "SSH_AUTH_SOCK", "TMUX", "TMUX_PANE", "TMUX_TMPDIR"} {
		if got[banned] {
			t.Errorf("host worker inherited %s", banned)
		}
	}
	for _, want := range []string{"PATH", "HOME", "ANTHROPIC_API_KEY"} {
		if !got[want] {
			t.Errorf("host worker lost %s", want)
		}
	}
}

// A task worker started by Native.Launch inherits only the allowlisted
// environment: no planted GITHUB_TOKEN or tmux client variables, but PATH,
// HOME and the provider's auth variable.
func TestNativeLaunchFiltersHostWorkerEnv(t *testing.T) {
	plantOwnerEnv(t)
	script := filepath.Join(t.TempDir(), "env-cli")
	if err := os.WriteFile(script, []byte(envDumpCLI), 0o755); err != nil {
		t.Fatal(err)
	}
	dump := filepath.Join(t.TempDir(), "env")
	task, wt := newTask(t)
	n := Native{Provider: "claude", Binary: script, Args: func(Request) []string { return []string{dump} }, Parse: parseNone}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	h, err := n.Launch(ctx, Request{Task: task, Worktree: wt, Brief: "dump env"})
	if err != nil {
		t.Fatal(err)
	}
	<-h.Done()
	checkWorkerEnv(t, dump)
}

// An interactive session started by LaunchSession is filtered the same way;
// the launch's own variables are added after the filter.
func TestLaunchSessionFiltersHostWorkerEnv(t *testing.T) {
	plantOwnerEnv(t)
	dump := filepath.Join(t.TempDir(), "env")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	h, err := LaunchSession(ctx, "claude", "/bin/sh", t.TempDir(), []string{"-c", "env > \"$0\"", dump}, []string{"BRIEFING_EXTRA=1"})
	if err != nil {
		t.Fatal(err)
	}
	<-h.Done()
	checkWorkerEnv(t, dump)
	data, _ := os.ReadFile(dump)
	if !strings.Contains(string(data), "BRIEFING_EXTRA=1") {
		t.Error("the launch's own variables were filtered")
	}
}
