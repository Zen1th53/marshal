package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/tmux"
)

// Started from a shell inside an unrelated tmux server (`tmux -L other`),
// marshal must still open its own session on its own server instead of
// stopping with a "running inside another tmux session" note.
func TestStartInsideForeignTmuxOpensOwnSession(t *testing.T) {
	if !statepath.PublicEngine {
		t.Skip("the governed edition keeps attaching to the operator's session")
	}
	tempDir := t.TempDir()
	logFile := filepath.Join(tempDir, "tmux_argv.log")
	fakeTmux := filepath.Join(tempDir, "tmux")
	script := fmt.Sprintf(`#!/bin/sh
echo "TMUX=${TMUX:-unset} PANE=${TMUX_PANE:-unset} $@" >> %q
case "$1" in
  has-session) exit 1 ;;
  display-message)
    if [ -n "$TMUX" ]; then printf 'work\tzsh\t@3\n'; fi
    exit 0 ;;
  *) exit 0 ;;
esac
`, logFile)
	if err := os.WriteFile(fakeTmux, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(fakeTmux)
	defer tmux.ResetBinaryPath()

	t.Setenv("TMUX", filepath.Join(tempDir, "other-sock")+",4242,0")
	t.Setenv("TMUX_PANE", "%7")
	t.Setenv("MARSHAL_TEST_FORCE_TMUX_LAUNCH", "1")

	repo := cliRepo(t)
	var stdout, stderr bytes.Buffer
	if code := Execute(context.Background(), repo.Path(), []string{"init"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("init failed: %s", stderr.String())
	}
	stderr.Reset()
	if code := Execute(context.Background(), repo.Path(), []string{"tui"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("tui failed: code=%d stderr=%s", code, stderr.String())
	}
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	session := tmux.SessionName(repo.Path())
	var newAt, attachAt string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "new-session -d -s "+session) {
			newAt = line
		}
		if strings.Contains(line, "attach-session -t "+session) {
			attachAt = line
		}
	}
	if newAt == "" || attachAt == "" {
		t.Fatalf("marshal did not open and attach its own session inside a foreign tmux:\n%s", data)
	}
	for _, line := range []string{newAt, attachAt} {
		if !strings.HasPrefix(line, "TMUX=unset PANE=unset ") {
			t.Fatalf("own-session call still carries the foreign tmux environment: %q", line)
		}
	}
	if os.Getenv("TMUX") != "" || os.Getenv("TMUX_PANE") != "" {
		t.Fatalf("foreign TMUX/TMUX_PANE left in the environment")
	}
}

// Inside MARSHAL's own server the enclosing session is kept, never nested.
func TestLeaveForeignTmuxKeepsOwnServer(t *testing.T) {
	t.Setenv("TMUX", tmux.OwnSocketPath()+",1,0")
	t.Setenv("TMUX_PANE", "%1")
	if tmux.LeaveForeignTmux(context.Background(), t.TempDir()) {
		t.Fatal("MARSHAL's own tmux server was treated as foreign")
	}
	if os.Getenv("TMUX") == "" {
		t.Fatal("TMUX removed inside MARSHAL's own server")
	}
}
