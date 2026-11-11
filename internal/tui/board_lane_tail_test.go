package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/tui/board/anim"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	"github.com/Zen1th53/marshal/internal/tui/board/model"
)

// fakeTailer serves pane captures from a map and counts the calls.
type fakeTailer struct {
	mu    sync.Mutex
	text  map[string]string
	err   map[string]error
	calls map[string]int
}

func newFakeTailer() *fakeTailer {
	return &fakeTailer{text: map[string]string{}, err: map[string]error{}, calls: map[string]int{}}
}

func (f *fakeTailer) Capture(pane string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[pane]++
	return f.text[pane], f.err[pane]
}

func (f *fakeTailer) set(pane, text string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.text[pane], f.err[pane] = text, err
}

func (f *fakeTailer) count(pane string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[pane]
}

func (f *fakeTailer) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		n += c
	}
	return n
}

func TestLaneTailLines(t *testing.T) {
	for _, tc := range []struct {
		name  string
		raw   string
		n, w  int
		want  []string
		check func(t *testing.T, got []string)
	}{
		{name: "last three", raw: "one\ntwo\nthree\nfour\n", n: 3, w: 40, want: []string{"two", "three", "four"}},
		{name: "ansi and osc", raw: "\x1b[1;31mred\x1b[0m text\n\x1b]0;title\x07after osc\n\x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\\n\x1b7saved\x1b8", n: 4, w: 40, want: []string{"red text", "after osc", "link", "saved"}},
		{name: "tabs", raw: "a\tb\n\tc", n: 3, w: 40, want: []string{"a       b", "        c"}},
		{name: "trailing blanks", raw: "work\n\n   \n\t\n\r\n", n: 3, w: 40, want: []string{"work"}},
		{name: "only blanks", raw: "\n \n\t\n", n: 3, w: 40, want: nil},
		{name: "carriage return and controls", raw: "line\r\nbell\a here\n", n: 3, w: 40, want: []string{"line", "bell  here"}},
		{name: "width cut", raw: "0123456789abcdef", n: 3, w: 8, want: []string{"0123456…"}},
		{name: "wide runes", raw: "界界界界界界", n: 3, w: 7, check: func(t *testing.T, got []string) {
			if len(got) != 1 || VisibleLen(got[0]) > 7 || !strings.HasSuffix(got[0], "…") || !strings.HasPrefix(got[0], "界界界") {
				t.Fatalf("wide cut = %q", got)
			}
		}},
		{name: "secret", raw: "export OPENAI_API_KEY=sk-proj-abcdefghijklmnop1234567890\nok", n: 3, w: 200, check: func(t *testing.T, got []string) {
			joined := strings.Join(got, "\n")
			if strings.Contains(joined, "abcdefghijklmnop1234567890") || len(got) != 2 || got[1] != "ok" {
				t.Fatalf("secret not redacted: %q", got)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tailLines(tc.raw, tc.n, tc.w)
			if tc.check != nil {
				tc.check(t, got)
				return
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("tailLines = %q, want %q", got, tc.want)
			}
		})
	}
	if lastBytes("aaa\nbbb\nccc", 5) != "ccc" || lastBytes("abc", 5) != "abc" {
		t.Fatal("lastBytes")
	}
	big := strings.Repeat("x", laneTailMax) + "\nend"
	if got := tailLines(lastBytes(big, laneTailMax), 3, 10); !slices.Equal(got, []string{"end"}) {
		t.Fatalf("capped capture = %q", got)
	}
}

// tailWorkspace is the live Board with two running workers on %3 and %4 and
// a controllable clock.
func tailWorkspace(t *testing.T) (*Workspace, *fakeTailer, *time.Time) {
	t.Helper()
	w, _ := boardWorkspace(t)
	f := newFakeTailer()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	w.laneTail.tailer = f
	w.laneTail.now = func() time.Time { return now }
	return w, f, &now
}

func TestLaneTailSamplerThrottle(t *testing.T) {
	w, f, now := tailWorkspace(t)
	f.set("%3", "build\ncompiling a\ncompiling b\nok\n", nil)
	f.set("%4", "claude\n> thinking\n", nil)
	if w.laneTailWant() != 0 {
		t.Fatal("tails wanted before any sample")
	}
	w.sampleLaneTails(f)
	if got := w.laneTailFor("%3"); !slices.Equal(got, []string{"compiling a", "compiling b", "ok"}) {
		t.Fatalf("tail %%3 = %q", got)
	}
	if got := w.laneTailFor("%4"); !slices.Equal(got, []string{"claude", "> thinking"}) {
		t.Fatalf("tail %%4 = %q", got)
	}
	if w.laneTailWant() != 2*laneTailRows || laneTailRows != 3 {
		t.Fatalf("laneTailWant = %d", w.laneTailWant())
	}
	if w.laneTailFor("%9") != nil {
		t.Fatal("unknown pane has a tail")
	}
	// The returned tail is a copy.
	w.laneTailFor("%3")[0] = "changed"
	if w.laneTailFor("%3")[0] != "compiling a" {
		t.Fatal("laneTailFor shares its slice")
	}

	// Within the second no pane is captured again.
	f.set("%3", "newer\n", nil)
	*now = now.Add(500 * time.Millisecond)
	w.sampleLaneTails(f)
	w.sampleLaneTails(f)
	if f.count("%3") != 1 || f.count("%4") != 1 {
		t.Fatalf("captured %d and %d times within a second", f.count("%3"), f.count("%4"))
	}
	// A second later both are captured once more and the repaint is asked.
	select {
	case <-w.laneNudge:
	default:
	}
	*now = now.Add(600 * time.Millisecond)
	w.sampleLaneTails(f)
	if f.count("%3") != 2 || f.count("%4") != 2 {
		t.Fatalf("captured %d and %d times after a second", f.count("%3"), f.count("%4"))
	}
	if got := w.laneTailFor("%3"); !slices.Equal(got, []string{"newer"}) {
		t.Fatalf("tail %%3 after update = %q", got)
	}
	select {
	case <-w.laneNudge:
	default:
		t.Fatal("changed tail did not nudge the lanes")
	}
}

func TestLaneTailErrorClearsTail(t *testing.T) {
	w, f, now := tailWorkspace(t)
	f.set("%3", "working\n", nil)
	f.set("%4", "other\n", nil)
	w.sampleLaneTails(f)
	if w.laneTailFor("%3") == nil {
		t.Fatal("no tail")
	}
	f.set("%3", "stale if shown\n", errors.New("pane gone"))
	*now = now.Add(time.Second)
	w.sampleLaneTails(f)
	if got := w.laneTailFor("%3"); got != nil {
		t.Fatalf("tail after capture error = %q", got)
	}
	if w.laneTailFor("%4") == nil || w.laneTailWant() != laneTailRows {
		t.Fatal("other lane lost its tail")
	}
	f.set("%4", "", errors.New("gone too"))
	*now = now.Add(time.Second)
	w.sampleLaneTails(f)
	if w.laneTailWant() != 0 {
		t.Fatal("tails wanted with no tail left")
	}
}

func TestLaneTailFinishedPaneDropped(t *testing.T) {
	w, f, now := tailWorkspace(t)
	f.set("%3", "working\n", nil)
	f.set("%4", "working\n", nil)
	w.sampleLaneTails(f)
	w.tmuxMu.Lock()
	w.tmuxActiveWins["task-T-017"].state = "done"
	w.tmuxActiveWins["task-T-018"].state = "failed"
	w.tmuxMu.Unlock()
	*now = now.Add(100 * time.Millisecond)
	w.sampleLaneTails(f)
	if w.laneTailFor("%3") != nil || w.laneTailFor("%4") != nil || w.laneTailWant() != 0 {
		t.Fatal("finished panes keep their tails")
	}
	if f.count("%3") != 1 {
		t.Fatal("finished pane captured")
	}
}

func TestLaneTailHiddenBoardSamplesNothing(t *testing.T) {
	w, f, _ := tailWorkspace(t)
	f.set("%3", "working\n", nil)
	w.sampleLaneTails(f)
	w.mu.Lock()
	w.screen = nil
	w.mu.Unlock()
	w.sampleLaneTails(f)
	if w.laneTailFor("%3") != nil || f.count("%3") != 1 {
		t.Fatal("hidden Board kept or captured tails")
	}
}

func TestLaneTailStopsOnCancel(t *testing.T) {
	w, f, _ := tailWorkspace(t)
	w.laneTail.now = nil
	w.laneTail.every = time.Millisecond
	f.set("%3", "tick\n", nil)
	ctx, cancel := context.WithCancel(t.Context())
	w.startLaneTail(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for f.total() < 2 || !slices.Equal(w.laneTailFor("%3"), []string{"tick"}) {
		if time.Now().After(deadline) {
			t.Fatal("sampler never ran")
		}
		time.Sleep(time.Millisecond)
	}
	if got := w.laneTailFor("%3"); !slices.Equal(got, []string{"tick"}) {
		t.Fatalf("live tail = %q", got)
	}
	cancel()
	select {
	case <-w.laneTail.done:
	case <-time.After(5 * time.Second):
		t.Fatal("sampler did not stop")
	}
	n := f.total()
	time.Sleep(20 * time.Millisecond)
	if f.total() != n {
		t.Fatal("sampler captured after stopping")
	}
}

// tailFrame paints the Home Board with two running lanes on %3 and %4 and
// the workspace's tails.
func tailFrame(w *Workspace, cols, rows int) []string {
	now := time.Date(2026, 11, 2, 14, 2, 31, 0, time.UTC)
	b := demoBoard(now)
	var lanes []model.Lane
	for _, l := range b.Lanes {
		switch l.TaskID {
		case "T-017":
			l.Pane = "%3"
		case "T-018":
			l.Pane = "%4"
		default:
			continue
		}
		lanes = append(lanes, l)
	}
	b.Lanes = lanes
	b.Run.ActiveLanes, b.Run.Running = 2, 2
	th := NewTheme(ThemeNoColor, true, false)
	v := boardVisual{board: b, now: now, theme: th, frame: anim.Frame{}, focus: layout.Tasks, edition: "Marshal", health: map[string]string{"codex": "●", "claude": "●", "agy": "◐", "opencode": "●"}}
	v.tails = w.laneTailFor
	v.tailWant = w.laneTailWant()
	l := layout.ComputeFor(cols, rows, layout.Home, v.layoutCounts())
	return paintBoard(l, th, v.render)
}

func TestLaneTailFrames(t *testing.T) {
	w, f, _ := tailWorkspace(t)
	f.set("%3", "$ go test ./internal/mcp/...\n\x1b[32mok\x1b[0m  internal/mcp\t0.41s\n=== RUN   TestMemoryRecall\n", nil)
	f.set("%4", "● Reading internal/app/memory_candidates.go\n  token=ghp_abcdefghijklmnopqrstuvwxyz0123\n● Editing memory_candidates.go\n", nil)
	w.sampleLaneTails(f)
	if w.laneTailWant() != 2*laneTailRows {
		t.Fatalf("laneTailWant = %d", w.laneTailWant())
	}

	frame := tailFrame(w, 120, 40)
	if len(frame) != 40 {
		t.Fatalf("%d rows", len(frame))
	}
	for y, line := range frame {
		if VisibleLen(line) != 120 {
			t.Fatalf("row %d: %d cells", y, VisibleLen(line))
		}
	}
	text := strings.Join(frame, "\n")
	// Each lane's captured lines appear below its own row, indented.
	for _, want := range []string{"  $ go test ./internal/mcp/...", "  ok  internal/mcp        0.41s", "  === RUN   TestMemoryRecall", "  ● Reading internal/app/memory_candidates.go", "  ● Editing memory_candidates.go"} {
		if !strings.Contains(text, want) {
			t.Fatalf("120x40 frame lacks %q:\n%s", want, text)
		}
	}
	if !strings.Contains(text, "ACTIVITY") {
		t.Fatalf("tails pushed the activity pane out:\n%s", text)
	}
	if strings.Contains(text, "abcdefghijklmnopqrstuvwxyz0123") {
		t.Fatal("secret on the Board")
	}
	checkGolden(t, "lane-tail/home-120x40", frame)

	small := strings.Join(tailFrame(w, 80, 24), "\n")
	for _, tail := range []string{"TestMemoryRecall", "Editing memory_candidates", "internal/mcp/..."} {
		if strings.Contains(small, tail) {
			t.Fatalf("80x24 frame shows tail %q:\n%s", tail, small)
		}
	}
}

func TestLaneTailRendersForTwoAndThreeLanes(t *testing.T) {
	for _, count := range []int{2, 3} {
		t.Run(fmt.Sprintf("%d lanes", count), func(t *testing.T) {
			w, _, _ := tailWorkspace(t)
			now := time.Date(2026, 11, 2, 14, 2, 31, 0, time.UTC)
			b := demoBoard(now)
			panes := []string{"%3", "%4", "%5"}
			want := []string{"tail one", "tail two", "tail three"}
			for i, task := range []string{"T-017", "T-018", "T-019"} {
				if i >= count {
					continue
				}
				for j := range b.Lanes {
					if b.Lanes[j].TaskID == task {
						b.Lanes[j].Pane = panes[i]
					}
				}
				w.laneTail.mu.Lock()
				if w.laneTail.tails == nil {
					w.laneTail.tails = make(map[string][]string)
				}
				w.laneTail.tails[panes[i]] = []string{want[i]}
				w.laneTail.mu.Unlock()
			}
			selected := map[string]bool{"T-017": true, "T-018": true, "T-019": count == 3}
			b.Lanes = slices.DeleteFunc(b.Lanes, func(l model.Lane) bool { return !selected[l.TaskID] })
			v := boardVisual{board: b, now: now, theme: NewTheme(ThemeNoColor, true, false), focus: layout.Tasks, health: map[string]string{}}
			v.tails = w.laneTailFor
			v.tailWant = count * laneTailRows
			frame := paintBoard(layout.ComputeFor(120, 40, layout.Home, v.layoutCounts()), v.theme, v.render)
			joined := strings.Join(frame, "\n")
			for i := 0; i < count; i++ {
				if !strings.Contains(joined, "  "+want[i]) {
					t.Fatalf("missing lane %d tail %q:\n%s", i+1, want[i], joined)
				}
			}
		})
	}
}
