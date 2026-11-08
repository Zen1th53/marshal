package tmux

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockHelpersArgv(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "argv.log")
	fake := filepath.Join(dir, "tmux")
	script := fmt.Sprintf(`#!/bin/sh
echo "$@" >> %q
case "$*" in
  *"-t %%%%9 #{pane_id}"*) printf '%%%%0 @0 200 100\n' ;;
  *"#{pane_id} #{window_id} #{window_width} #{pane_width}"*) printf '%%%%7 @3 201 100\n' ;;
esac
exit 0
`, logFile)
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	SetBinaryPath(fake)
	defer ResetBinaryPath()
	ctx := context.Background()

	if err := DockPane(ctx, "%7", "%0", 100); err != nil {
		t.Fatal(err)
	}
	if err := DockPane(ctx, "%7", "%0", 0); err == nil {
		t.Fatal("a zero dock width was accepted")
	}
	if err := DockPane(ctx, "", "%0", 10); err == nil {
		t.Fatal("an empty source was accepted")
	}
	if err := UndockPane(ctx, "%7", "marshal-chat-1"); err != nil {
		t.Fatal(err)
	}
	if err := ResizePaneWidth(ctx, "%7", 56); err != nil {
		t.Fatal(err)
	}
	g, err := QueryPaneGeometry(ctx, "%7")
	if err != nil || g != (PaneGeometry{WindowID: "@3", WindowWidth: 201, PaneWidth: 100}) {
		t.Fatalf("geometry %+v %v", g, err)
	}
	// tmux answering for another pane means the asked-for one is gone.
	if _, err := QueryPaneGeometry(ctx, "%9"); err == nil {
		t.Fatal("geometry of a vanished pane was accepted")
	}
	if err := UnbindKeyTable(ctx, "marshal-dock-x"); err != nil {
		t.Fatal(err)
	}
	if err := UnbindKeyTable(ctx, " "); err == nil {
		t.Fatal("an empty table was accepted")
	}
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"join-pane -h -b -d -l 100 -s %7 -t %0",
		"break-pane -d -s %7 -n marshal-chat-1",
		"resize-pane -t %7 -x 56",
		"display-message -p -t %7 #{pane_id} #{window_id} #{window_width} #{pane_width}",
		"display-message -p -t %9 #{pane_id} #{window_id} #{window_width} #{pane_width}",
		"unbind-key -q -a -T marshal-dock-x",
	}, "\n") + "\n"
	if string(data) != want {
		t.Fatalf("argv:\n%s\nwant:\n%s", data, want)
	}
}

// TestBindMouseKeysCopiesRootMouse copies a real server's root mouse
// bindings into a private table and leaves every other root key alone.
func TestBindMouseKeysCopiesRootMouse(t *testing.T) {
	bin, err := FindBinary()
	if err != nil {
		t.Skip("tmux not installed")
	}
	sockDir, err := os.MkdirTemp("/tmp", "tmx-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sockDir)
	sock := filepath.Join(sockDir, "s")
	wrapper := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec "+bin+" -f /dev/null -S "+sock+" \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	SetBinaryPath(wrapper)
	defer ResetBinaryPath()
	ctx := context.Background()
	if _, err := RunCommand(ctx, "new-session", "-d", "-s", "m", "sleep", "60"); err != nil {
		t.Fatal(err)
	}
	defer RunCommand(context.Background(), "kill-server")
	if err := BindMouseKeys(ctx, "marshal-test"); err != nil {
		t.Fatal(err)
	}
	out, err := RunCommand(ctx, "list-keys", "-T", "marshal-test")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, want := range []string{"MouseDown1Pane", "select-pane -t =", "WheelUpPane", "MouseDrag1Pane"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in copied table:\n%s", want, got)
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
		if key, _ := rootBindingKey(strings.Replace(line, "-T marshal-test ", "-T root ", 1)); !isMouseKey(key) {
			t.Fatalf("copied a key that is not the mouse: %s", line)
		}
	}
	if err := BindMouseKeys(ctx, "bad table"); err == nil {
		t.Fatal("a table name with a space was accepted")
	}
}

func TestIsMouseKey(t *testing.T) {
	for key, want := range map[string]bool{"MouseDown1Pane": true, "M-MouseDrag1Pane": true, "C-WheelUpStatus": true, "DoubleClick1Pane": true, "F11": false, "M-Left": false, "C-b": false} {
		if isMouseKey(key) != want {
			t.Fatalf("isMouseKey(%q) != %v", key, want)
		}
	}
}
