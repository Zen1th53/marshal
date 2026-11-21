package v2

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

var update = flag.Bool("update-v2-golden", false, "rewrite testdata/*.golden")

var esc = regexp.MustCompile("\x1b\\[[0-9;]*[a-zA-Z]")

func strip(s string) string { return esc.ReplaceAllString(s, "") }

type screenCase struct {
	n          int
	name       string
	rows       int
	board      func() vm.Board
	pane       func(w, h int) []string
	paneW      func(cols int) int
	paneHeight func(rows int) int
}

var screens = []screenCase{
	{1, "running", 31, fixtureRunning, nil, nil, nil},
	{2, "ready", 22, fixtureReady, nil, nil, nil},
	{3, "planning", 22, fixturePlanning, nil, nil, nil},
	{4, "decision", 22, fixtureDecision, nil, nil, nil},
	{5, "paused", 19, fixturePaused, nil, nil, nil},
	{6, "chat", 26, fixtureChat, func(w, h int) []string { return claudeCode(w, h, false) }, func(c int) int { return c - 2 }, func(r int) int { return r - 7 }},
	{7, "split", 30, fixtureSplit, func(w, h int) []string { return claudeCode(w, h, false) }, func(c int) int { return SplitChatW(c) - 2 }, func(r int) int { return r - 7 }},
}

func (sc screenCase) render(cols, rows int, th Theme) []string {
	var pane []string
	if sc.pane != nil {
		pane = sc.pane(sc.paneW(cols), sc.paneHeight(rows))
	}
	return RenderChat(sc.board(), cols, rows, th, pane)
}

// deliberate lists cells that differ from the target on purpose, by screen
// and row, with why.
var deliberate = map[int]map[int]string{
	// The Run bar: the design draws 3 active cells for one active task of 12
	// in 24 cells, which no whole number of tasks gives; v2 draws 2.
	5: {12: "run bar rounds to whole tasks"},
	// Keys the Board does not act on in that state are not drawn: on a
	// run p starts a message and l focuses the lanes (pause and limits stay
	// in the Space palette); v re-checks only on the readiness screen; no
	// path switches the Marshal to a faster model mid-step, so f is not
	// offered; and only the browser accepts an architecture.
	1: {10: "no v re-check on the Workers pane", 29: "no l limits, p pause all"},
	3: {16: "no f faster model hint", 20: "no f faster model"},
	4: {16: "no f faster model hint", 18: "no ⏎ accept here", 20: "no f faster model"},
}

// TestScreensMatchDesign renders each fixture at 132 columns in truecolor
// and compares the cells with docs/design/board-v2/screen*.ans.
func TestScreensMatchDesign(t *testing.T) {
	for _, sc := range screens {
		t.Run(sc.name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "docs", "design", "board-v2", fmt.Sprintf("screen%d.ans", sc.n)))
			if err != nil {
				t.Fatal(err)
			}
			want := strings.Split(strings.TrimSuffix(strip(string(raw)), "\n"), "\n")
			got := sc.render(132, sc.rows, Theme{Color: ColorTrue, Unicode: true})
			if len(got) != len(want) {
				t.Fatalf("rows: got %d, want %d", len(got), len(want))
			}
			for i := range want {
				g := strip(got[i])
				if g == want[i] {
					continue
				}
				if why, ok := deliberate[sc.n][i]; ok {
					t.Logf("row %d differs on purpose (%s)", i, why)
					continue
				}
				t.Errorf("row %d:\n got %q\nwant %q", i, g, want[i])
			}
		})
	}
}

// TestScreenGoldens keeps the stripped cells of every screen at 132 columns
// (Unicode and ASCII), 100 and 80 columns in testdata.
func TestScreenGoldens(t *testing.T) {
	for _, sc := range screens {
		for _, v := range []struct {
			tag  string
			cols int
			th   Theme
		}{
			{"132", 132, Theme{Color: ColorTrue, Unicode: true}},
			{"132-ascii", 132, Theme{Color: ColorNone, Unicode: false}},
			{"100", 100, Theme{Color: Color256, Unicode: true}},
			{"080", 80, Theme{Color: Color256, Unicode: true}},
		} {
			name := fmt.Sprintf("screen%d-%s-%s", sc.n, sc.name, v.tag)
			t.Run(name, func(t *testing.T) {
				lines := sc.render(v.cols, sc.rows, v.th)
				var b strings.Builder
				for _, l := range lines {
					s := strip(l)
					if n := len([]rune(s)); n != v.cols {
						t.Errorf("row width %d, want %d: %q", n, v.cols, s)
					}
					if !v.th.Unicode {
						for _, r := range s {
							if r > 0x7f {
								t.Errorf("non-ASCII %q in ASCII render: %q", r, s)
								break
							}
						}
					}
					b.WriteString(s + "|\n")
				}
				path := filepath.Join("testdata", name+".golden")
				if *update {
					if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				want, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("%v (run with -update-v2-golden)", err)
				}
				if b.String() != string(want) {
					t.Errorf("%s differs:\n%s", path, b.String())
				}
			})
		}
	}
}

// TestZeroBoardNeverPanics draws every screen from an empty view model at
// many sizes.
func TestZeroBoardNeverPanics(t *testing.T) {
	for _, s := range []vm.Screen{"", vm.ScreenReady, vm.ScreenPlanning, vm.ScreenRunning, vm.ScreenDecision, vm.ScreenPaused, vm.ScreenChat, vm.ScreenSplit} {
		for _, size := range [][2]int{{1, 1}, {20, 5}, {60, 12}, {80, 24}, {100, 30}, {132, 31}, {200, 60}} {
			b := vm.Board{Screen: s}
			if s == vm.ScreenDecision {
				b.Decision = &vm.Decision{}
			}
			if s == vm.ScreenPaused {
				b.Paused = &vm.Paused{}
			}
			for _, th := range []Theme{{ColorTrue, true}, {Color256, true}, {ColorMono, false}, {ColorNone, false}} {
				lines := Render(b, size[0], size[1], th)
				if len(lines) != size[1] {
					t.Fatalf("%s %v: %d lines", s, size, len(lines))
				}
				RenderSide(b, size[0], size[1], th)
			}
		}
	}
	lines := Render(vm.Board{}, 132, 31, Theme{Color: ColorNone, Unicode: true})
	if !strings.Contains(strings.Join(lines, "\n"), dash) {
		t.Fatal("an empty Board shows no dash")
	}
}

// TestRenderSizes checks every fixture fills exactly cols x rows at the
// sizes the Board meets, in every colour mode.
func TestRenderSizes(t *testing.T) {
	for _, sc := range screens {
		for _, cols := range []int{60, 79, 80, 100, 119, 120, 132, 160, 220} {
			for _, rows := range []int{12, 24, 40} {
				for _, th := range []Theme{{ColorTrue, true}, {Color256, true}, {ColorMono, true}, {ColorNone, false}} {
					lines := sc.render(cols, rows, th)
					if len(lines) != rows {
						t.Fatalf("%s %dx%d: %d rows", sc.name, cols, rows, len(lines))
					}
					for _, l := range lines {
						if n := len([]rune(strip(l))); n != cols {
							t.Fatalf("%s %dx%d: width %d", sc.name, cols, rows, n)
						}
					}
				}
			}
		}
	}
}
