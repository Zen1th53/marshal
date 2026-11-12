package tmux

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A private tmux server started in a directory that is later deleted must not
// break new launches: each new session, window and respawn starts in its
// explicit project root. Uses a real tmux on an isolated socket directory.
func TestLaunchesSurviveDeletedServerDirectory(t *testing.T) {
	bin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not installed")
	}
	base, err := os.MkdirTemp("", "mxcwd")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)
	sock, gone, root := filepath.Join(base, "s"), filepath.Join(base, "gone"), filepath.Join(base, "root")
	for _, dir := range []string{sock, gone, root} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TMUX_TMPDIR", sock)
	// tmux prefers $TMUX over TMUX_TMPDIR: inside a live tmux pane the bare
	// kill-server below would otherwise stop the caller's own server.
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	ctx := context.Background()
	defer exec.Command(bin, "kill-server").Run()
	// Start the server from inside the directory that is then deleted.
	start := exec.Command(bin, "new-session", "-d", "-s", "first", "sleep 60")
	start.Dir = gone
	if out, err := start.CombinedOutput(); err != nil {
		t.Skipf("tmux server unavailable: %v %s", err, out)
	}
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	probe := func(name string) []string {
		return []string{"/bin/sh", "-c", `pwd -P > "$0"; sleep 30`, filepath.Join(root, name)}
	}
	if err := NewSession(ctx, "second", root, "main", probe("session.out")); err != nil {
		t.Fatal(err)
	}
	if err := NewWindow(ctx, "second", "win", root, nil, probe("window.out")); err != nil {
		t.Fatal(err)
	}
	if err := RespawnWindowIn(ctx, "first:0", root, probe("respawn.out")); err != nil {
		t.Fatal(err)
	}
	if err := NewWindow(ctx, "second", "shell", root, nil, []string{`pwd -P > shell.out; sleep 30`}); err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(root)
	for _, name := range []string{"session.out", "window.out", "respawn.out", "shell.out"} {
		var got string
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			if data, err := os.ReadFile(filepath.Join(root, name)); err == nil && len(data) > 0 {
				got = strings.TrimSpace(string(data))
				break
			}
		}
		if got != want {
			t.Fatalf("%s started in %q, want %q", name, got, want)
		}
	}
}

// A session Marshal starts runs its tmux client from the root directory, so a
// tmux server it starts never lives in a project directory that may later be
// deleted (every pane would then open with the shell's "error retrieving
// current directory" lines).
func TestNewSessionStartsServerFromRoot(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "cwd.log")
	fake := filepath.Join(dir, "tmux")
	script := "#!/bin/sh\npwd -P >> '" + logFile + "'\nexit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	SetBinaryPath(fake)
	defer ResetBinaryPath()
	if err := NewSession(context.Background(), "s", dir, "main", []string{"sleep", "1"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	first := strings.SplitN(strings.TrimSpace(string(data)), "\n", 2)[0]
	if first != "/" {
		t.Fatalf("the tmux client that may start the server ran in %q, want /", first)
	}
}
