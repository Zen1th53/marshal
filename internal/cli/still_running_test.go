package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/tmux"
)

// After this terminal detaches, the launcher says that Marshal keeps
// running and how to return; after a quit (the session or its control
// centre gone) or a failed attach it says nothing.
func TestNoteStillRunningAfterDetach(t *testing.T) {
	for _, c := range []struct {
		name      string
		session   bool
		paneDead  string
		attachErr error
		want      bool
	}{
		{"detached", true, "0 0", nil, true},
		{"quit ended the session", false, "", nil, false},
		{"control centre exited", true, "1 0", nil, false},
		{"attach failed", true, "0 0", errors.New("boom"), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			has := "exit 1"
			if c.session {
				has = "exit 0"
			}
			script := "#!/bin/sh\ncase \"$1\" in\n  has-session) " + has + ";;\n  display-message) printf '%s\\n' '" + c.paneDead + "'; exit 0;;\nesac\nexit 0\n"
			fake := filepath.Join(dir, "tmux")
			if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			tmux.SetBinaryPath(fake)
			t.Cleanup(tmux.ResetBinaryPath)
			var out bytes.Buffer
			cmd := &command{stdout: &out}
			cmd.noteStillRunning(context.Background(), "marshal-test", c.attachErr)
			if got := strings.Contains(out.String(), stillRunningNotice); got != c.want {
				t.Fatalf("notice printed %v, want %v: %q", got, c.want, out.String())
			}
		})
	}
}
