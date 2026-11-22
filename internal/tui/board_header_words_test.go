package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/tui/board/model"
)

// The header state chip is the run's state in the owner's words, first match
// wins over the table in the spec.
func TestHeaderStateWords(t *testing.T) {
	th := NewTheme(ThemeDefault, true, true)
	for _, c := range []struct {
		name string
		v    boardVisual
		want string
	}{
		{"waiting-stop", boardVisual{theme: th, board: model.Board{Run: model.RunHead{State: marshal.AwaitingUser}}}, "waiting for you"},
		{"paused", boardVisual{theme: th, note: "stopped: worker exited 2", stopped: true, board: model.Board{Run: model.RunHead{State: marshal.Dispatching}}}, "paused"},
		{"user pause hold", boardVisual{theme: th, board: model.Board{Run: model.RunHead{State: marshal.Dispatching, UserPaused: true}}}, "paused"},
		{"owner-waits", boardVisual{theme: th, guard: &guardAlert{}, board: model.Board{Run: model.RunHead{State: marshal.Dispatching}}}, "waiting for you"},
		{"done", boardVisual{theme: th, board: model.Board{Run: model.RunHead{State: marshal.Closed}}}, "done"},
		{"planning", boardVisual{theme: th, board: model.Board{Run: model.RunHead{State: marshal.Drafting}}}, "planning"},
		{"starting", boardVisual{theme: th, board: model.Board{Run: model.RunHead{State: ""}}}, "starting"},
		{"running", boardVisual{theme: th, board: model.Board{Run: model.RunHead{State: marshal.Dispatching}}}, "running"},
	} {
		if got := StripANSI(c.v.stateChip()); got != c.want {
			t.Errorf("%s: stateChip %q, want %q", c.name, got, c.want)
		}
	}
}

// The header names the run by its title, never by its id.
func TestHeaderHidesRunID(t *testing.T) {
	th := NewTheme(ThemeNoColor, true, true)
	v := boardVisual{theme: th, now: time.Now(), edition: "Marshal", board: model.Board{Run: model.RunHead{
		ID: "RUN-0123456789abcdef0123456789abcdef", Title: "Add login", State: marshal.Dispatching,
	}}}
	for _, w := range []int{80, 100, 160, 200} {
		got := StripANSI(v.header(w))
		if !strings.Contains(got, "Add login") {
			t.Errorf("header %d lacks the title: %q", w, got)
		}
		if strings.Contains(got, "RUN-") || strings.Contains(got, "R-") {
			t.Errorf("header %d shows the run id: %q", w, got)
		}
	}
}
