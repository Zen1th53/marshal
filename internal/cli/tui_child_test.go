package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/tmux"
)

func TestTUIChildFailureReachesParentTerminal(t *testing.T) {
	for _, earlyExit := range []bool{false, true} {
		t.Run(fmt.Sprintf("exits-before-attach=%t", earlyExit), func(t *testing.T) {
			dir := gitDirectory(t)
			if _, stderr, code := runCLI(t, dir, "init"); code != 0 {
				t.Fatal(stderr)
			}
			fake := filepath.Join(t.TempDir(), "tmux")
			// Execute exactly the command tmux receives, after invalidating the
			// policy between the parent's preflight and the child's startup.
			// Attach itself succeeds, just as real tmux does after [exited].
			selectCode := 0
			if earlyExit {
				selectCode = 1
			}
			script := fmt.Sprintf(`#!/bin/sh
case "$1" in
 has-session) exit 1 ;;
 new-session)
   rm -- %q
   shift 8
   MARSHAL_TEST_TUI_PROCESS=1 TMUX=isolated MARSHAL_TEST_TMUX_BINARY="$0" "$@"
   exit 0 ;;
 select-window) exit %d ;;
 attach-session) echo '[exited]'; exit 0 ;;
esac
`, filepath.Join(dir, "CAPABILITIES.yaml"), selectCode)
			if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			tmux.SetBinaryPath(fake)
			defer tmux.ResetBinaryPath()
			t.Setenv("TMUX", "")
			t.Setenv("MARSHAL_TEST_FORCE_TMUX_LAUNCH", "1")
			stdout, stderr, code := runCLI(t, dir)
			if code == 0 {
				t.Fatalf("child failure returned success: %q %q", stdout, stderr)
			}
			for _, want := range []string{"TUI child exited with status", "Marshal isn't set up in this project yet.", "Run `marshal init` here."} {
				if !strings.Contains(stderr, want) {
					t.Fatalf("missing %q in stderr: %s", want, stderr)
				}
			}
			if !earlyExit && !strings.Contains(stdout, "[exited]") {
				t.Fatalf("did not attach: %s", stdout)
			}
		})
	}
}
