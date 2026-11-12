package tui

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/usage"
)

// TestStatusLineOwnedSessionOnly: in a session Marshal made, the status line
// carries the vitals row and, once the chat is docked, the title row; the
// [m] click is bound only in the dock's own key tables (never root, never a
// key the chat types), and quit unbinds it and gives the status line back.
// In a session the operator made nothing of this is installed and the titles
// stay on the pane borders.
func TestStatusLineOwnedSessionOnly(t *testing.T) {
	for _, owned := range []bool{true, false} {
		t.Run(map[bool]string{true: "owned", false: "operator's"}[owned], func(t *testing.T) {
			f := newDockFake(t, 160)
			if owned {
				f.write("owned", "1")
			}
			w, chat := dockWorkspace(t)
			f.pane(chat.paneID, "@1", 160)
			chat.provider = "codex"
			now := time.Now()
			w.storeUsage([]usage.Quota{{Provider: "codex", Known: true, AsOf: now, Model: "gpt-6.1-sol",
				Windows: []usage.Window{codexWindow(now)}}})
			ctx := context.Background()
			dt, bt := dockKeyTable(w.providerRoot()), boardDockKeyTable(w.providerRoot())
			if err := w.reconcileDock(ctx); err != nil {
				t.Fatal(err)
			}
			if err := w.reconcileStatus(ctx, true); err != nil {
				t.Fatal(err)
			}
			log := f.take()
			mustNotContain(t, log, "bind-key -T root", "bind-key -n", "bind-key -T "+dt+" m ", "bind-key -T "+bt+" m ")
			if !owned {
				mustNotContain(t, log, "status-format", dockClickKey)
				mustContain(t, log, "pane-border-status top", "@marshal_dock_title")
				if w.vitalsInStatus() {
					t.Fatal("an operator's session reports the vitals in its status line")
				}
				return
			}
			mustContain(t, log,
				"set-option -t ="+w.tmuxSession+": status-position top",
				"set-option -t ="+w.tmuxSession+": status-format[0] ",
				"set-option -t ="+w.tmuxSession+": status-format[1] #{?#{==:#{window_id},@0},",
				"set-option -t ="+w.tmuxSession+": status 2",
				"bind-key -T "+dt+" "+dockClickKey+" if-shell -F ",
				"bind-key -T "+bt+" "+dockClickKey+" if-shell -F ",
				"select-pane -t %0 ; send-keys -t %0 -H 1b 5b 39 30 30 31 7e",
				"GPT-6.1-Sol",
			)
			mustNotContain(t, log, "pane-border-status top", "@marshal_dock_title")
			if !w.vitalsInStatus() {
				t.Fatal("the status line does not report the vitals row")
			}
			// The click lands on [m]: the binding's range is where the title
			// puts it, three cells of marker in.
			title, at := w.dockTitleAt(chat, dockWidth(160), true)
			x0 := 3 + at
			if !strings.HasPrefix(string([]rune(title)[at:]), "[m]") ||
				!strings.Contains(log, "#{e|>=:#{mouse_x},"+strconv.Itoa(x0)+"},#{e|<:#{mouse_x},"+strconv.Itoa(x0+3)+"}") {
				t.Fatalf("click range for [m] at %d in %q not bound:\n%s", x0, title, log)
			}

			// Quit: the dock unbinds its tables, the status line is restored.
			w.dock.enabled = false
			if err := w.reconcileDock(ctx); err != nil {
				t.Fatal(err)
			}
			w.endStatus()
			log = f.take()
			mustContain(t, log, "unbind-key -q -a -T "+dt, "unbind-key -q -a -T "+bt,
				"-t ="+w.tmuxSession+": status-format[0]", "-t ="+w.tmuxSession+": status-position")
			if w.vitalsInStatus() {
				t.Fatal("vitals still reported in the status line after quit")
			}
			// Undocked, the status line is back to the vitals row alone.
			if d := w.status.dock.Load(); d != nil {
				t.Fatalf("dock title row kept after undock: %+v", d)
			}
		})
	}
}

// TestBoardWithoutVitalsWhenStatusCarriesThem: with the vitals row on the
// tmux status line the Board, the picker, the opening and the Blocked screen
// paint none of their own and keep their layout one row up.
func TestBoardWithoutVitalsWhenStatusCarriesThem(t *testing.T) {
	w := NewWorkspace(nil, "test-proj", "test-session")
	w.workDir = t.TempDir()
	w.homeBoard = true
	th := NewTheme(ThemeNoColor, true, false)
	w.theme = th
	with, _, _ := w.boardFrameVitals(w.state, th, nil, 120, 40, false)
	without, _, _ := w.boardFrameVitals(w.state, th, nil, 120, 40, true)
	if len(with) != 40 || len(without) != 40 {
		t.Fatalf("rows %d/%d", len(with), len(without))
	}
	if !strings.Contains(StripANSI(with[0]), "running") {
		t.Fatalf("the Board's own vitals row is missing: %q", StripANSI(with[0]))
	}
	if strings.Contains(StripANSI(without[0]), "running") || StripANSI(without[0]) != StripANSI(with[1]) {
		t.Fatalf("with the status line the header should be on top:\n%q\n%q", StripANSI(without[0]), StripANSI(with[1]))
	}
}
