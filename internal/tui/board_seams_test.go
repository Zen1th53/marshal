package tui

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

func TestSeams(t *testing.T) {
	w := &Workspace{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.startLaneTail(ctx)
	w.startAttention(ctx)
	w.startUsage(ctx)
	w.notifyCommand([]string{"status"})
	if w.laneTailFor("pane") != nil || w.laneTailWant() != 0 || w.openTaskDiff("task") || w.taskDiff.HandleKey(w, KeyEvent{Type: KeyRune, Rune: 'D'}) || w.taskDiff.StatusLine(80) != "" {
		t.Fatal("hooks must be inert")
	}
	if a, b := w.usageSegment(80); a != "" || b != "" {
		t.Fatal("usage must be empty")
	}
	t.Run("bell", func(t *testing.T) {
		var out bytes.Buffer
		term := NewTerminal(strings.NewReader(""), &out)
		if err := term.Bell(); err != nil {
			t.Fatal(err)
		}
		if out.String() != "\a" {
			t.Fatalf("bell = %q", out.String())
		}
	})
	t.Run("raw diff", func(t *testing.T) {
		dv := NewDiffViewer(nil, "")
		dv.OpenRaw("Task changes", "diff --git a/a b/a\n--- a/a\n+++ b/a\n@@ -1 +1 @@\n-old\n+new\ndiff --git a/b b/b\n--- a/b\n+++ b/b\n@@ -1 +1 @@\n-old\n+new\n")
		if !dv.IsOpen() || len(dv.files) != 2 {
			t.Fatal("raw diff not opened")
		}
		rendered := strings.Join(dv.Render(100, 30), "\n")
		if !strings.Contains(rendered, "Task changes") || strings.Contains(rendered, "staged") {
			t.Fatalf("unexpected raw view: %s", rendered)
		}
	})
	t.Run("inspector diff key", func(t *testing.T) {
		ws, _ := boardWorkspace(t)
		ws.boardUI.focus, ws.boardUI.task, ws.boardUI.inspect = layout.Tasks, "T-017", "T-017"
		if ws.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 'D'}) {
			t.Fatal("inert task diff must fall through")
		}
		if ws.boardUI.inspect != "T-017" || ws.diffViewer.IsOpen() {
			t.Fatal("inert task diff changed inspector")
		}
	})
	t.Run("notify command", func(t *testing.T) {
		reply, err := NewCommandHandler(w).Handle(ctx, "/notify status")
		if err != nil || !strings.Contains(reply, "Notifications") {
			t.Fatalf("notify: %q %v", reply, err)
		}
	})
	t.Run("empty visual hooks", func(t *testing.T) {
		v := boardViewVisual(memEpoch, NewTheme(ThemeDefault, true, true))
		hooked := v
		hooked.tails = w.laneTailFor
		hooked.tailWant = w.laneTailWant()
		hooked.usageWide, hooked.usageNarrow = w.usageSegment(120)
		pane := layout.Pane{Rect: layout.Rect{W: 120, H: 12}}
		if !reflect.DeepEqual(v.lanes(pane), hooked.lanes(pane)) || !reflect.DeepEqual(v.layoutCounts(), hooked.layoutCounts()) {
			t.Fatal("empty tails changed lanes")
		}
		for _, width := range []int{80, 100, 120, 200} {
			if v.header(width) != hooked.header(width) {
				t.Fatal("empty usage changed header")
			}
		}
	})
}
