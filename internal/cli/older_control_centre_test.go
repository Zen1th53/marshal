package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/tmux"
)

// olderSeams stands in for the tmux calls of olderControlCentreElsewhere:
// one older control centre runs on socket "other".
type olderSeams struct {
	confirm         bool
	killed          []string
	attached        []string
	killErr         error
	held            int
	foundOnSockets  []string
	sessionsChecked []string
}

func (s *olderSeams) install(t *testing.T) {
	t.Helper()
	sockets, find, kill, attach, held, confirm := olderControlCentreSockets, olderControlCentreFind, olderControlCentreKill, olderControlCentreAttach, olderControlCentreHeld, olderControlCentreConfirm
	t.Cleanup(func() {
		olderControlCentreSockets, olderControlCentreFind, olderControlCentreKill, olderControlCentreAttach, olderControlCentreHeld, olderControlCentreConfirm = sockets, find, kill, attach, held, confirm
	})
	olderControlCentreSockets = func() []string { return []string{"/tmp/old-tmpdir/tmux-1/default"} }
	olderControlCentreFind = func(_ context.Context, session string, sockets []string) string {
		s.sessionsChecked = append(s.sessionsChecked, session)
		s.foundOnSockets = sockets
		return sockets[0]
	}
	olderControlCentreKill = func(_ context.Context, socket, session string) error {
		s.killed = append(s.killed, socket+" "+session)
		return s.killErr
	}
	olderControlCentreAttach = func(_ context.Context, socket, session string, _ io.Reader, _, _ io.Writer) error {
		s.attached = append(s.attached, socket+" "+session)
		return nil
	}
	olderControlCentreHeld = func(string) bool {
		s.held++
		return s.held < 3
	}
	olderControlCentreConfirm = func(*command) bool { return s.confirm }
}

// Finding 73: restarting `marshal codex` while an older control centre ran
// on another tmux server opened only a bare Codex chat. Marshal finds it,
// and on the owner's yes stops it and waits for it to let go of the project
// before the new one starts.
func TestOlderControlCentreElsewhereReplacedOnYes(t *testing.T) {
	s := &olderSeams{confirm: true}
	s.install(t)
	var errOut bytes.Buffer
	c := &command{stdin: strings.NewReader(""), stderr: &errOut}
	attached, err := c.olderControlCentreElsewhere(context.Background(), t.TempDir(), "marshal-proj-abc")
	if attached || err != nil {
		t.Fatalf("attached=%v err=%v, want the caller to start the new control centre", attached, err)
	}
	if len(s.killed) != 1 || s.killed[0] != "/tmp/old-tmpdir/tmux-1/default marshal-proj-abc" {
		t.Fatalf("killed = %v", s.killed)
	}
	if s.held < 3 {
		t.Fatalf("did not wait for the older control centre to release the project (%d checks)", s.held)
	}
	if len(s.attached) != 0 {
		t.Fatalf("attached to the replaced control centre: %v", s.attached)
	}
}

// On no, or without a terminal to ask on, the terminal attaches to the older
// control centre, so the owner still gets a Board rather than a bare chat.
func TestOlderControlCentreElsewhereAttachedOnNo(t *testing.T) {
	s := &olderSeams{confirm: false}
	s.install(t)
	var errOut bytes.Buffer
	c := &command{stdin: strings.NewReader(""), stderr: &errOut}
	attached, err := c.olderControlCentreElsewhere(context.Background(), t.TempDir(), "marshal-proj-abc")
	if !attached || err != nil {
		t.Fatalf("attached=%v err=%v, want an attach to the older one", attached, err)
	}
	if len(s.killed) != 0 {
		t.Fatalf("stopped the older control centre without a yes: %v", s.killed)
	}
	if len(s.attached) != 1 || !strings.HasPrefix(s.attached[0], "/tmp/old-tmpdir/tmux-1/default ") {
		t.Fatalf("attached = %v", s.attached)
	}
	if !strings.Contains(errOut.String(), olderControlCentreNotice) {
		t.Fatalf("no notice: %q", errOut.String())
	}
}

// A failed stop falls back to attaching to the older control centre.
func TestOlderControlCentreElsewhereKillFailureAttaches(t *testing.T) {
	s := &olderSeams{confirm: true, killErr: fmt.Errorf("no server")}
	s.install(t)
	var errOut bytes.Buffer
	c := &command{stdin: strings.NewReader(""), stderr: &errOut}
	if attached, _ := c.olderControlCentreElsewhere(context.Background(), t.TempDir(), "s"); !attached || len(s.attached) != 1 {
		t.Fatalf("attached=%v %v", attached, s.attached)
	}
	if !strings.Contains(errOut.String(), "Could not stop the older control centre") {
		t.Fatalf("stop failure not said: %q", errOut.String())
	}
}

// Nothing elsewhere: the launch goes on as before.
func TestNoOlderControlCentreElsewhere(t *testing.T) {
	s := &olderSeams{}
	s.install(t)
	olderControlCentreFind = func(context.Context, string, []string) string { return "" }
	c := &command{stdin: strings.NewReader(""), stderr: io.Discard}
	if attached, err := c.olderControlCentreElsewhere(context.Background(), t.TempDir(), "s"); attached || err != nil {
		t.Fatalf("attached=%v err=%v", attached, err)
	}
	if len(s.killed)+len(s.attached) != 0 {
		t.Fatal("acted with no older control centre")
	}
}

func TestConfirmReplaceOlderDefaultsToYes(t *testing.T) {
	for input, want := range map[string]bool{"\n": true, "y\n": true, "n\n": false, "": false} {
		var errOut bytes.Buffer
		c := &command{stdin: strings.NewReader(input), stderr: &errOut}
		if got := c.confirmReplaceOlder(); got != want {
			t.Errorf("%q: replace = %v, want %v", input, got, want)
		}
		if !strings.Contains(errOut.String(), "[Y/n]") || !strings.Contains(errOut.String(), "another tmux server") {
			t.Errorf("%q: question not asked plainly: %q", input, errOut.String())
		}
	}
}

// The launch outside tmux asks before opening a new session: with an older
// control centre elsewhere and no yes, it attaches there and never starts a
// second session beside it.
func TestLaunchAttachesToOlderControlCentreElsewhere(t *testing.T) {
	tempDir := t.TempDir()
	logFile := filepath.Join(tempDir, "tmux_argv.log")
	fakeTmux := filepath.Join(tempDir, "tmux")
	script := fmt.Sprintf("#!/bin/sh\necho \"$@\" >> %q\ncase \"$1\" in\n  has-session) exit 1 ;;\n  *) exit 0 ;;\nesac\n", logFile)
	if err := os.WriteFile(fakeTmux, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(fakeTmux)
	defer tmux.ResetBinaryPath()
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	t.Setenv("MARSHAL_TEST_FORCE_TMUX_LAUNCH", "1")
	s := &olderSeams{confirm: false}
	s.install(t)

	repo := cliRepo(t)
	var stdout, stderr bytes.Buffer
	if code := Execute(context.Background(), repo.Path(), []string{"init"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("init failed: %s", stderr.String())
	}
	stderr.Reset()
	if code := Execute(context.Background(), repo.Path(), []string{"tui"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("tui failed: code=%d stderr=%s", code, stderr.String())
	}
	session := tmux.SessionName(repo.Path())
	if len(s.sessionsChecked) != 1 || s.sessionsChecked[0] != session {
		t.Fatalf("looked for %v, want %s", s.sessionsChecked, session)
	}
	if len(s.attached) != 1 {
		t.Fatalf("did not attach to the older control centre: %v", s.attached)
	}
	data, _ := os.ReadFile(logFile)
	if strings.Contains(string(data), "new-session") {
		t.Fatalf("a second control centre was started beside the older one:\n%s", data)
	}
}
