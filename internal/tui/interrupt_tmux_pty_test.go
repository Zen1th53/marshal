//go:build linux

package tui

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Use tmux's real PTY and terminal emulator: raw output replay cannot prove
// that an unaddressed newline did not scroll the terminal underneath Screen.
func TestPTYEmptyComposerCtrlCKeepsScreenInRealTmux(t *testing.T) {
	tmuxBin, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	bin := buildMarshalBinary(t)
	project := initProject(t, bin)
	socket := filepath.Join(t.TempDir(), "tmux.sock")
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.Command(tmuxBin, append([]string{"-S", socket}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	t.Cleanup(func() { exec.Command(tmuxBin, "-S", socket, "kill-server").Run() })
	run("-f", "/dev/null", "new-session", "-d", "-s", "interrupt", "-x", "80", "-y", "24", "-c", project)
	run("set-option", "-w", "remain-on-exit", "on")
	run("respawn-pane", "-k", "-t", "interrupt:0", bin, "tui")
	capture := func() string { return run("capture-pane", "-p", "-t", "interrupt:0") }
	wait := func(check func(string) bool) string {
		t.Helper()
		deadline := time.Now().Add(ptyWait(10 * time.Second))
		for time.Now().Before(deadline) {
			text := capture()
			if check(text) {
				return text
			}
			time.Sleep(30 * time.Millisecond)
		}
		t.Fatalf("terminal did not reach expected state:\n%s", capture())
		return ""
	}
	wait(func(s string) bool { return strings.Contains(s, "MARSHAL") && strings.Contains(s, PromptMarker) })
	for i := 0; i < 12; i++ {
		run("send-keys", "-t", "interrupt:0", "C-c")
		text := wait(func(s string) bool { return strings.Contains(s, "Press Ctrl+C again to exit, or /quit.") })
		if strings.Count(text, PromptMarker) != 1 || strings.Count(text, "MARSHAL") != 1 {
			t.Fatalf("Ctrl+C accumulated workspace chrome:\n%s", text)
		}
		if !strings.HasPrefix(strings.TrimSpace(strings.Split(text, "\n")[22]), "Press Ctrl+C") || strings.TrimSpace(strings.Split(text, "\n")[23]) != PromptMarker {
			t.Fatalf("warning must occupy the status row, leaving one empty composer:\n%s", text)
		}
		// A non-text key disarms exit without submitting or changing the draft.
		run("send-keys", "-t", "interrupt:0", "Left")
		wait(func(s string) bool { return !strings.Contains(s, "Press Ctrl+C") })
	}
	run("send-keys", "-t", "interrupt:0", "C-c")
	wait(func(s string) bool { return strings.Contains(s, "Press Ctrl+C") })
	run("send-keys", "-t", "interrupt:0", "C-c")
	deadline := time.Now().Add(ptyWait(10 * time.Second))
	for time.Now().Before(deadline) {
		if strings.TrimSpace(run("display-message", "-p", "-t", "interrupt:0", "#{pane_dead}:#{pane_dead_status}")) == "1:0" {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("double Ctrl+C did not exit successfully:\n%s", capture())
}
