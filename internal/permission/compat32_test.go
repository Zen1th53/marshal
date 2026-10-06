package permission

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/tmux"
)

func TestTmux32PermissionWindowKeysAndClosure(t *testing.T) {
	for _, key := range []string{"A", "D", "\n", "\x1b", "a", "x", "", "closed", "timeout"} {
		t.Run(key, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("REVIEW_FIXTURE", dir)
			os.WriteFile(filepath.Join(dir, "key"), []byte(key), 0600)
			fake := filepath.Join(dir, "tmux")
			script := `#!/bin/bash
printf '%s\n' "$*" >> "$REVIEW_FIXTURE/commands"
case "$1" in
 display-message)
  if [[ ${@: -1} == '#{version}' ]]; then echo 3.2a; else exit 1; fi ;;
 new-window)
  if [[ $(cat "$REVIEW_FIXTURE/key") == closed ]]; then echo %review; exit; fi
  if [[ $(cat "$REVIEW_FIXTURE/key") == timeout ]]; then
   sleep .1 | /bin/bash "${@: -1}" > "$REVIEW_FIXTURE/screen"
  else
   /bin/bash "${@: -1}" < "$REVIEW_FIXTURE/key" > "$REVIEW_FIXTURE/screen"
  fi
  echo %review ;;
 select-window|kill-window) exit 0 ;;
 *) exit 1 ;;
esac
`
			if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			tmux.SetBinaryPath(fake)
			defer tmux.ResetBinaryPath()
			allow, err := Popup(context.Background(), "session", []Request{{Object: "exact object", Scope: "this session only"}}, 30*time.Millisecond)
			if err != nil || allow != (key == "A") {
				t.Fatalf("key=%q allow=%v err=%v", key, allow, err)
			}
			commands, _ := os.ReadFile(filepath.Join(dir, "commands"))
			if strings.Contains(string(commands), "display-popup") || !strings.Contains(string(commands), "new-window -d -P") || !strings.Contains(string(commands), "kill-window -t %review") {
				t.Fatalf("unsafe commands: %s", commands)
			}
			if key != "closed" {
				screen, _ := os.ReadFile(filepath.Join(dir, "screen"))
				if !strings.Contains(string(screen), "exact object") || !strings.Contains(string(screen), "A = Allow") {
					t.Fatalf("missing review: %s", screen)
				}
			}
		})
	}
}
