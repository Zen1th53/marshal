package v2

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

// narrowBoard is the Board as seen live in a 65-column Board pane: a dev
// build with its hash, a long state word, a run, workers and an inbox.
func narrowBoard(screen vm.Screen, state string) vm.Board {
	b := fixtureRunning()
	b.Screen = screen
	b.Top = vm.TopBar{Version: "v0.3.5-dev+ba8b86c7", Project: "marshal-work-with-a-long-name", Run: "review-fixes",
		State: state, StateTone: vm.ToneVio, Workers: 8, Inbox: 3, Clock: at(12, 2)}
	b.NowLine = vm.NowLine{Marshal: "claude Opus · watching reviews", Work: "8 running · 4 queued",
		FromYou: "nothing · next ask: final review", FromYouOK: true}
	return b
}

// checkTopRow asserts the state word and the clock are both on row 0,
// whole, with at least one blank cell between them, and that the row is
// exactly cols wide.
func checkTopRow(t *testing.T, row, state string, cols int) {
	t.Helper()
	if n := len([]rune(row)); n != cols {
		t.Fatalf("top row is %d cells, want %d: %q", n, cols, row)
	}
	i := strings.Index(row, " "+state+" ")
	if i < 0 {
		t.Fatalf("state %q not whole on the top row: %q", state, row)
	}
	j := strings.LastIndex(row, "12:02")
	if j < 0 {
		t.Fatalf("no clock on the top row: %q", row)
	}
	if end := i + len(" "+state+" "); j <= end {
		t.Fatalf("clock not apart from the state (state ends %d, clock at %d): %q", end, j, row)
	}
}

// checkFromYou asserts the "from you" text is on the row whole, or cut
// with an ellipsis, never broken mid-word without one.
func checkFromYou(t *testing.T, row, want string, unicode bool) {
	t.Helper()
	_, got, ok := strings.Cut(row, "from you ")
	if !ok {
		t.Fatalf("no from you on the NOW row: %q", row)
	}
	got = strings.TrimRight(got, " |")
	if !unicode {
		want = ascii(want)
	}
	ell := "…"
	if !unicode {
		ell = "..."
	}
	if got == want {
		return
	}
	if !strings.HasSuffix(got, ell) || !strings.HasPrefix(want, strings.TrimSuffix(got, ell)) {
		t.Fatalf("from you cut without an ellipsis: %q (want %q)", got, want)
	}
}

func TestTopBarKeepsStateAndClockAtAnyWidth(t *testing.T) {
	themes := []Theme{{Color: ColorNone, Unicode: true}, {Color: ColorNone, Unicode: false}}
	for _, state := range []string{"REVIEWING", "YOUR TURN", "2 PAUSED", "RUNNING"} {
		for _, cols := range []int{40, 50, 65, 80, 100, 132} {
			for _, th := range themes {
				full := strip(Render(narrowBoard(vm.ScreenRunning, state), cols, 30, th)[0])
				checkTopRow(t, full, state, cols)
				split := strip(Render(narrowBoard(vm.ScreenSplit, state), cols, 30, th)[0])
				checkTopRow(t, split, state, cols)
				side := RenderSide(narrowBoard(vm.ScreenSplit, state), cols, 30, th)
				checkTopRow(t, strip(side[0]), state, cols)
			}
		}
	}
}

// The live row at 65 columns: "REV12:02" no more, and the build hash is
// what goes first.
func TestTopBarNarrowDropsBuildSuffixFirst(t *testing.T) {
	row := strip(RenderSide(narrowBoard(vm.ScreenSplit, "REVIEWING"), 65, 30, Theme{Unicode: true})[0])
	if strings.Contains(row, "+ba8b86c7") {
		t.Fatalf("build suffix kept at 65 columns: %q", row)
	}
	if strings.Contains(row, "REV12") {
		t.Fatalf("state runs into the clock: %q", row)
	}
	// Wide, everything stays.
	row = strip(Render(narrowBoard(vm.ScreenRunning, "REVIEWING"), 190, 30, Theme{Unicode: true})[0])
	for _, want := range []string{"v0.3.5-dev+ba8b86c7", "project marshal-work-with-a-long-name", "run review-fixes", "8 workers", "inbox 3", "claude · opus-5.5"} {
		if !strings.Contains(row, want) {
			t.Fatalf("no %q at 190 columns: %q", want, row)
		}
	}
}

func TestNowLineKeepsFromYouReadable(t *testing.T) {
	for _, cols := range []int{40, 50, 65, 80, 100, 132} {
		for _, th := range []Theme{{Unicode: true}, {Unicode: false}} {
			for _, fy := range []string{"nothing · next ask: final review", "press ⏎ to move 2 tasks to claude", "nothing"} {
				b := narrowBoard(vm.ScreenRunning, "RUNNING")
				b.NowLine.FromYou = fy
				full := strip(Render(b, cols, 30, th)[1])
				checkFromYou(t, full, fy, th.Unicode)
				side := strip(RenderSide(b, cols, 30, th)[1])
				checkFromYou(t, side, fy, th.Unicode)
				if cols >= 65 {
					want := fy
					if !th.Unicode {
						want = ascii(fy)
					}
					if !strings.Contains(full, want) || !strings.Contains(side, want) {
						t.Fatalf("%d cols: from you not whole:\n%q\n%q", cols, full, side)
					}
				}
			}
		}
	}
}

func TestCutWords(t *testing.T) {
	c := newCanvas(1, 1, true)
	for _, tc := range []struct {
		s    string
		n    int
		want string
	}{
		{"nothing · next ask: final review", 40, "nothing · next ask: final review"},
		{"nothing · next ask: final review", 20, "nothing · next ask…"},
		{"nothing · next ask: final review", 12, "nothing…"},
		{"8 running · 4 queued", 15, "8 running · 4…"},
		{"supercalifragilistic", 8, "superca…"},
	} {
		if got := c.cutWords(tc.s, tc.n); got != tc.want {
			t.Errorf("cutWords(%q, %d) = %q, want %q", tc.s, tc.n, got, tc.want)
		}
	}
	a := newCanvas(1, 1, false)
	if got := a.cutWords("press ⏎ to move 2 tasks", 16); got != "press Enter..." {
		t.Errorf("ASCII cutWords = %q", got)
	}
}
