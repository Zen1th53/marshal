package tmux

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectNamingAndCollisionAvoidance(t *testing.T) {
	projA := "/home/user/work/project-alpha"
	projB := "/home/user/work/project-beta"

	sessA := SessionName(projA)
	sessB := SessionName(projB)
	if sessA == sessB {
		t.Fatalf("sessions collided for distinct projects: %s vs %s", sessA, sessB)
	}

	winCodexA := WindowName("codex", projA)
	winCodexB := WindowName("codex", projB)
	if winCodexA == winCodexB {
		t.Fatalf("windows collided for distinct projects: %s vs %s", winCodexA, winCodexB)
	}

	winClaudeA := WindowName("claude", projA)
	if winCodexA == winClaudeA {
		t.Fatalf("different providers produced same window name in project A: %s vs %s", winCodexA, winClaudeA)
	}

	// Stability check
	if SessionName(projA) != sessA {
		t.Fatalf("SessionName is not stable across calls")
	}
	if WindowName("codex", projA) != winCodexA {
		t.Fatalf("WindowName is not stable across calls")
	}
}

func TestDetectInstallCommand(t *testing.T) {
	cmd := DetectInstallCommand()
	if cmd == "" {
		t.Fatalf("expected non-empty install command")
	}
	valid := strings.Contains(cmd, "pacman") ||
		strings.Contains(cmd, "apt") ||
		strings.Contains(cmd, "dnf") ||
		strings.Contains(cmd, "zypper") ||
		strings.Contains(cmd, "brew")
	if !valid {
		t.Fatalf("unexpected install command: %s", cmd)
	}
}

func TestFakeTmuxArgvRecording(t *testing.T) {
	tempDir := t.TempDir()
	logFile := filepath.Join(tempDir, "tmux_argv.log")
	fakeTmux := filepath.Join(tempDir, "tmux")

	script := fmt.Sprintf(`#!/bin/sh
echo "$@" >> %q
case "$1" in
  has-session)
    exit 0
    ;;
  display-message)
    case "$*" in
      *"#{session_name} #{window_name} #{window_id}"*)
        printf 'test-session test-win @0\n'
        exit 0
        ;;
      *"#{pane_dead}"*)
        printf '1\n'
        exit 0
        ;;
    esac
    exit 0
    ;;
  list-windows)
    printf 'marshal\nmarshal-codex-12345678\n'
    exit 0
    ;;
  capture-pane)
    printf 'terminal evidence line 1\nterminal evidence line 2\n'
    exit 0
    ;;
  *)
    exit 0
    ;;
esac
`, logFile)

	if err := os.WriteFile(fakeTmux, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	SetBinaryPath(fakeTmux)
	defer ResetBinaryPath()

	ctx := context.Background()

	// 1. HasSession
	if !HasSession(ctx, "my-session") {
		t.Fatalf("expected HasSession to return true")
	}

	// 2. NewSession
	if err := NewSession(ctx, "my-session", "/tmp", "marshal", []string{"marshal", "tui"}); err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	// 3. CurrentSessionAndWindow
	sess, win, winID, err := CurrentSessionAndWindow(ctx)
	if err != nil || sess != "test-session" || win != "test-win" || winID != "@0" {
		t.Fatalf("CurrentSessionAndWindow returned unexpected: %s, %s, %s, err=%v", sess, win, winID, err)
	}

	// 4. ListWindows & WindowExists
	wins, err := ListWindows(ctx, "test-session")
	if err != nil || len(wins) != 2 {
		t.Fatalf("ListWindows returned %v, err=%v", wins, err)
	}
	exists, err := WindowExists(ctx, "test-session", "marshal-codex-12345678")
	if err != nil || !exists {
		t.Fatalf("WindowExists returned %v, err=%v", exists, err)
	}

	// 5. SelectWindow
	if err := SelectWindow(ctx, "marshal-codex-12345678"); err != nil {
		t.Fatalf("SelectWindow failed: %v", err)
	}

	// 6. NewWindow
	if err := NewWindow(ctx, "test-session", "marshal-claude", "/tmp", []string{"FOO=bar"}, []string{"claude"}); err != nil {
		t.Fatalf("NewWindow failed: %v", err)
	}

	// 7. KillWindow
	if err := KillWindow(ctx, "marshal-claude"); err != nil {
		t.Fatalf("KillWindow failed: %v", err)
	}

	// 8. CapturePane
	cap, err := CapturePane(ctx, "marshal-codex-12345678")
	if err != nil || !strings.Contains(cap, "terminal evidence") {
		t.Fatalf("CapturePane failed: %v, cap=%q", err, cap)
	}

	// 9. SetPaneReadOnly
	if err := SetPaneReadOnly(ctx, "marshal-codex-12345678", true); err != nil {
		t.Fatalf("SetPaneReadOnly failed: %v", err)
	}

	// 10. JoinPane & BreakPane
	if err := JoinPane(ctx, "marshal-codex-12345678", "marshal", true); err != nil {
		t.Fatalf("JoinPane failed: %v", err)
	}
	if err := BreakPane(ctx, "marshal-codex-12345678"); err != nil {
		t.Fatalf("BreakPane failed: %v", err)
	}

	// 11. SetStatusText
	if err := SetStatusText(ctx, "test-session", "status-text"); err != nil {
		t.Fatalf("SetStatusText failed: %v", err)
	}

	// 12. BindGlobalKey & UnbindGlobalKey
	if err := BindGlobalKey(ctx, "F11", "select-window -t marshal"); err != nil {
		t.Fatalf("BindGlobalKey failed: %v", err)
	}
	if err := UnbindGlobalKey(ctx, "F11"); err != nil {
		t.Fatalf("UnbindGlobalKey failed: %v", err)
	}

	// 13. SetWindowOption & IsPaneDead
	if err := SetWindowOption(ctx, "marshal-codex-12345678", "remain-on-exit", "on"); err != nil {
		t.Fatalf("SetWindowOption failed: %v", err)
	}
	dead, err := IsPaneDead(ctx, "marshal-codex-12345678")
	if err != nil || !dead {
		t.Fatalf("IsPaneDead failed: %v, dead=%v", err, dead)
	}

	// Verify argv log contains key commands
	logData, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	logStr := string(logData)

	expectedCommands := []string{
		"has-session -t my-session",
		"new-session -d -s my-session -c /tmp -n marshal marshal tui",
		"display-message -p #{session_name} #{window_name} #{window_id}",
		"list-windows -t test-session -F #{window_name}",
		"select-window -t marshal-codex-12345678",
		"new-window -t test-session -n marshal-claude -c /tmp -e FOO=bar claude",
		"kill-window -t marshal-claude",
		"capture-pane -p -S - -t marshal-codex-12345678",
		"select-pane -t marshal-codex-12345678 -d",
		"join-pane -h -s marshal-codex-12345678 -t marshal",
		"break-pane -s marshal-codex-12345678",
		"set-option -t test-session status-right status-text",
		"bind-key -n F11 select-window -t marshal",
		"unbind-key -n F11",
		"set-option -w -t marshal-codex-12345678 remain-on-exit on",
		"display-message -p -t marshal-codex-12345678 #{pane_dead}",
	}

	for _, exp := range expectedCommands {
		if !strings.Contains(logStr, exp) {
			t.Errorf("argv log missing expected command %q\nFull log:\n%s", exp, logStr)
		}
	}
}

func TestEscapeTmuxArgs(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"normal", "normal"},
		{";", `\;`},
		{"echo hello;", `echo hello\;`},
		{"a;b", "a;b"},
		{"a;b;", `a;b\;`},
		{"", ""},
	}
	for _, tc := range cases {
		got := EscapeTmuxArg(tc.in)
		if got != tc.want {
			t.Errorf("EscapeTmuxArg(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	escaped := EscapeTmuxArgs([]string{"echo", "test;", ";", "arg"})
	expected := []string{"echo", `test\;`, `\;`, "arg"}
	if len(escaped) != len(expected) {
		t.Fatalf("length mismatch: %d vs %d", len(escaped), len(expected))
	}
	for i := range escaped {
		if escaped[i] != expected[i] {
			t.Errorf("arg[%d] = %q, want %q", i, escaped[i], expected[i])
		}
	}
}

func TestWindowAndSessionNaming(t *testing.T) {
	root := "/path/to/project with spaces"
	chatWin := ChatWindowName(root)
	taskWin := TaskWindowName("task-01", root)

	if !strings.HasPrefix(chatWin, "marshal-chat-") {
		t.Errorf("expected marshal-chat- prefix, got %q", chatWin)
	}
	if !strings.HasPrefix(taskWin, "marshal-task-task-01-") {
		t.Errorf("expected marshal-task-task-01- prefix, got %q", taskWin)
	}
}

func TestCurrentSessionAndWindow_Parsing(t *testing.T) {
	tempDir := t.TempDir()
	fakeTmux := filepath.Join(tempDir, "tmux")

	script := `#!/bin/sh
case "$*" in
  *"display-message"*"#{session_name}"*"#{window_name}"*"#{window_id}"*)
    printf 'my session with space\tmy window with space\t@42\n'
    exit 0
    ;;
  *"#{pane_dead} #{pane_dead_status}"*)
    printf '1 137\n'
    exit 0
    ;;
  *"#{pane_pid}"*)
    printf '12345\n'
    exit 0
    ;;
esac
exit 0
`
	if err := os.WriteFile(fakeTmux, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	SetBinaryPath(fakeTmux)
	defer ResetBinaryPath()

	ctx := context.Background()
	sess, win, winID, err := CurrentSessionAndWindow(ctx)
	if err != nil {
		t.Fatalf("CurrentSessionAndWindow failed: %v", err)
	}
	if sess != "my session with space" {
		t.Errorf("expected session 'my session with space', got %q", sess)
	}
	if win != "my window with space" {
		t.Errorf("expected window 'my window with space', got %q", win)
	}
	if winID != "@42" {
		t.Errorf("expected winID '@42', got %q", winID)
	}

	dead, exitCode, err := PaneDeadStatus(ctx, "%1")
	if err != nil || !dead || exitCode != 137 {
		t.Errorf("PaneDeadStatus: dead=%v exitCode=%d err=%v", dead, exitCode, err)
	}

	pid, _, err := PanePIDAndPGID(ctx, "%1")
	if err != nil || pid != 12345 {
		t.Errorf("PanePIDAndPGID: pid=%d err=%v", pid, err)
	}
}
