package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/tui/board/anim"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	"github.com/Zen1th53/marshal/internal/tui/board/model"
	"github.com/Zen1th53/marshal/internal/usage"
)

var usageNow = time.Date(2026, 11, 8, 18, 0, 0, 0, time.UTC)

func usageWin(label string, left float64) usage.Window {
	return usage.Window{Label: label, LeftPercent: left, ResetsAt: usageNow.Add(time.Hour)}
}

// usageFresh is a fresh Codex report and the three providers that expose
// nothing, as usage.Read gives them.
func usageFresh() []usage.Quota {
	return []usage.Quota{
		{Provider: "codex", Known: true, Windows: []usage.Window{usageWin("5h", 82), usageWin("wk", 49)}},
		{Provider: "claude", Reason: usage.NotExposed},
		{Provider: "opencode", Reason: usage.NotExposed},
		{Provider: "agy", Reason: usage.NotExposed},
	}
}

func usageWorkspace(qs []usage.Quota) *Workspace {
	w := &Workspace{}
	w.usage.quotas, w.usage.read = qs, true
	return w
}

func TestUsageSegmentEmptyBeforeReading(t *testing.T) {
	w := &Workspace{}
	if a, b := w.usageSegment(120); a != "" || b != "" {
		t.Fatalf("no reading yet must be empty: %q %q", a, b)
	}
}

func TestUsageSegmentStrings(t *testing.T) {
	partial := usageFresh()
	partial[0].Windows[0] = usage.Window{Label: "5h"}
	for _, tc := range []struct {
		name         string
		qs           []usage.Quota
		wide, narrow string
	}{
		{"fresh", usageFresh(), "codex 5h 82% · wk 49% left · claude unknown · opencode unknown · agy unknown", "cx 82/49 · cl ? · oc ? · ag ?"},
		{"codex unknown", append([]usage.Quota{{Provider: "codex", Reason: usage.ReasonStale}}, usageFresh()[1:]...), "codex unknown · claude unknown · opencode unknown · agy unknown", "cx ? · cl ? · oc ? · ag ?"},
		{"one window unknown", partial, "codex 5h unknown · wk 49% left · claude unknown · opencode unknown · agy unknown", "cx ?/49 · cl ? · oc ? · ag ?"},
		// Percent left rounds down, never up.
		{"rounds down", []usage.Quota{{Provider: "codex", Known: true, Windows: []usage.Window{usageWin("5h", 81.9), usageWin("60m", 0.5)}}}, "codex 5h 81% · 60m 0% left", "cx 81/0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wide, narrow := usageWorkspace(tc.qs).usageSegment(120)
			if wide != tc.wide || narrow != tc.narrow {
				t.Fatalf("got\n %q\n %q\nwant\n %q\n %q", wide, narrow, tc.wide, tc.narrow)
			}
		})
	}
}

// TestUsageFromCodexFile reads a real rollout file through usage.Read and
// shows it, so the header and the reader agree on what is known.
func TestUsageFromCodexFile(t *testing.T) {
	dir := t.TempDir()
	day := filepath.Join(dir, "2026", "11", "08")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":{"used_percent":18.0,"window_minutes":300,"resets_at":1794164929},"secondary":{"used_percent":51.0,"window_minutes":10080,"resets_at":1794665063}}}}` + "\n"
	path := filepath.Join(day, "rollout-a.jsonl")
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	mod := usageNow.Add(-time.Minute)
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
	wide, narrow := usageWorkspace(usage.Read(usageNow, dir)).usageSegment(120)
	if wide != "codex 5h 82% · wk 49% left · claude unknown · opencode unknown · agy unknown" || narrow != "cx 82/49 · cl ? · oc ? · ag ?" {
		t.Fatalf("fresh file: %q %q", wide, narrow)
	}
	// A day later the same file is stale: unknown, not the old percent.
	_, narrow = usageWorkspace(usage.Read(usageNow.Add(24*time.Hour), dir)).usageSegment(120)
	if narrow != "cx ? · cl ? · oc ? · ag ?" {
		t.Fatalf("stale file: %q", narrow)
	}
}

func TestUsageRefreshWhileBoardVisible(t *testing.T) {
	w, _ := boardWorkspace(t)
	var reads atomic.Int32
	w.usage.every = 5 * time.Millisecond
	w.usage.readFn = func(time.Time) []usage.Quota {
		reads.Add(1)
		return usageFresh()
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.startUsage(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for reads.Load() < 3 {
		if time.Now().After(deadline) {
			t.Fatal("usage not refreshed")
		}
		time.Sleep(time.Millisecond)
	}
	if wide, _ := w.usageSegment(120); !strings.HasPrefix(wide, "codex 5h 82%") {
		t.Fatalf("cached = %q", wide)
	}
	cancel()
	time.Sleep(20 * time.Millisecond)
	stopped := reads.Load()
	time.Sleep(30 * time.Millisecond)
	if reads.Load() != stopped {
		t.Fatal("usage still read after ctx done")
	}
}

func TestUsageNoReadWhileBoardHidden(t *testing.T) {
	// No screen: the Board is not visible, so nothing is read.
	w := &Workspace{}
	var reads atomic.Int32
	w.usage.every = 2 * time.Millisecond
	w.usage.readFn = func(time.Time) []usage.Quota { reads.Add(1); return usageFresh() }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.startUsage(ctx)
	time.Sleep(30 * time.Millisecond)
	if reads.Load() != 0 {
		t.Fatalf("read %d times while hidden", reads.Load())
	}
	if a, b := w.usageSegment(120); a != "" || b != "" {
		t.Fatal("hidden Board must keep no reading")
	}
}

func TestUsageStoreRepaintsOnChange(t *testing.T) {
	w := &Workspace{repaint: make(chan struct{}, 1)}
	w.storeUsage(usageFresh())
	select {
	case <-w.repaint:
	default:
		t.Fatal("first reading must repaint")
	}
	w.storeUsage(usageFresh())
	select {
	case <-w.repaint:
		t.Fatal("unchanged reading must not repaint")
	default:
	}
}

// TestUsageHeaderGolden is the header with the quota part: the 120x40 demo
// frame, where the run's own parts leave no room so the part gives way
// first, and the header line of a small run at widths where it fits.
func TestUsageHeaderGolden(t *testing.T) {
	th := NewTheme(ThemeNoColor, true, false)
	ws := usageWorkspace(usageFresh())
	v := boardVisual{board: demoBoard(memEpoch), now: memEpoch, theme: th, frame: anim.Frame{}, edition: "Marshal"}
	plain := v
	v.usageWide, v.usageNarrow = ws.usageSegment(120)
	l := layout.ComputeFor(120, 40, layout.Home, v.layoutCounts())
	frame := paintBoard(l, th, v.render)
	if strings.Join(frame, "\n") != strings.Join(paintBoard(l, th, plain.render), "\n") {
		t.Fatal("a quota part that does not fit must leave the frame unchanged")
	}
	checkGolden(t, "usage/header-120x40", frame)

	small := v
	small.board = model.Board{Run: model.RunHead{ID: "R-7f3a", Title: "usage", State: marshal.Dispatching, Done: 1, Total: 4, ActiveLanes: 2}}
	var lines []string
	for _, width := range []int{80, 100, 120, 160, 200} {
		lines = append(lines, small.header(width))
	}
	checkGolden(t, "usage/header-lines", lines)
	if !strings.Contains(lines[1], "cx 82/49 · cl ? · oc ? · ag ?") || !strings.Contains(lines[4], "codex 5h 82% · wk 49% left · claude unknown · opencode unknown · agy unknown") {
		t.Fatalf("quota part missing:\n%s", strings.Join(lines, "\n"))
	}
}
