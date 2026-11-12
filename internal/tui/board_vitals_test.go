package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/hostwatch"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	"github.com/Zen1th53/marshal/internal/tui/board/model"
	"github.com/Zen1th53/marshal/internal/usage"
)

var vitalsNow = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

// vitalsHost is the fixture host snapshot: two tick samples (10000/8500 then
// 11000/9270 → 23% busy), package 61 °C, 48 GiB of 100 used, and a root mount
// 71% full. lowDisk forces the low-disk flag.
func vitalsHost(lowDisk bool) *hostSnapshot {
	free, used := uint64(290e9), uint64(710e9)
	if lowDisk {
		free, used = uint64(15e9), uint64(985e9)
	}
	a := hostwatch.Sample{At: vitalsNow.Add(-time.Second), CPUTicksTotal: 10000, CPUTicksIdle: 8500, CPUTicksKnown: true}
	b := hostwatch.Sample{
		At:                     vitalsNow,
		CPUTicksTotal:          11000,
		CPUTicksIdle:           9270,
		CPUTicksKnown:          true,
		CPUPackageTempC:        61,
		CPUPackageTempCKnown:   true,
		MemTotalBytes:          100 << 20,
		MemTotalBytesKnown:     true,
		MemAvailableBytes:      52 << 20,
		MemAvailableBytesKnown: true,
		DiskMounts:             []hostwatch.DiskMountUsage{{MountPoint: "/", TotalBytes: 1000e9, UsedBytes: used, FreeBytes: free, Known: true}},
	}
	snap := &hostSnapshot{Now: vitalsNow, Samples: []hostwatch.Sample{a, b}, Policy: hostwatch.DefaultPolicy(), Active: map[string]bool{}}
	return snap
}

// vitalsFixture is the TestVitalsLineWidths fixture visual.
func vitalsFixture(th *Theme) boardVisual {
	qs := []usage.Quota{
		{Provider: "claude", Known: true, Windows: []usage.Window{{Label: "wk", LeftPercent: 58, ResetsAt: vitalsNow.Add(98 * time.Hour)}}, AsOf: vitalsNow.Add(-5 * time.Minute)},
		{Provider: "codex", Known: true, Windows: []usage.Window{{Label: "5h", LeftPercent: 88, ResetsAt: vitalsNow.Add(3*time.Hour + 30*time.Minute)}}, AsOf: vitalsNow.Add(-time.Minute)},
		{Provider: "agy", Reason: usage.NotExposed},
		{Provider: "opencode", Reason: usage.NotExposed},
	}
	return boardVisual{
		theme:  th,
		now:    vitalsNow,
		board:  model.Board{Run: model.RunHead{ActiveLanes: 3}},
		health: map[string]string{"claude": "ready", "codex": "ready", "agy": "re-verify"},
		host:   vitalsHost(false),
		vitals: vitalsData{quotas: qs, read: true, installed: map[string]bool{"claude": true, "codex": true, "agy": true, "opencode": true}},
	}
}

func TestVitalsLineWidths(t *testing.T) {
	v := vitalsFixture(NewTheme(ThemeNoColor, true, false))
	right := "cpu 23% · 61°C · ram 48% · disk 71% · 3 running "
	for _, tc := range []struct {
		w    int
		want string
	}{
		{160, " ✳ claude ▓▓░ 58% left ·4d · ⬡ codex ▓▓▓ 88% left ·3h · ◆ agy limit unknown · ● opencode limit unknown" + strings.Repeat(" ", 10) + right},
		{120, " ✳ claude ▓▓░ 58% left ·4d · ⬡ codex ▓▓▓ 88% left ·3h · ◆ — · ● —" + strings.Repeat(" ", 7) + right},
		{80, " ✳ 58% left · ⬡ 88% left · ◆ —" + strings.Repeat(" ", 2) + right},
		{56, " ✳ 58% left · ⬡ 88% left" + strings.Repeat(" ", 9) + "cpu 23% · 61°C · 3 run "},
	} {
		line, _ := v.vitalsLine(tc.w)
		if got := StripANSI(line); got != tc.want {
			t.Errorf("w=%d:\n got %q\nwant %q", tc.w, got, tc.want)
		}
		if n := VisibleLen(line); n != tc.w {
			t.Errorf("w=%d: visible width %d", tc.w, n)
		}
	}
}

func TestVitalsHonesty(t *testing.T) {
	th := NewTheme(ThemeNoColor, true, false)
	// No host watcher: the right block is only the running count.
	noHost := vitalsFixture(th)
	noHost.host = nil
	line, _ := noHost.vitalsLine(160)
	if got := strings.TrimRight(StripANSI(line), " "); !strings.HasSuffix(got, "3 running") || strings.Contains(got, "cpu") {
		t.Fatalf("no host watcher: %q", got)
	}
	// One host sample: cpu is unknown.
	oneSample := vitalsFixture(th)
	snap := *oneSample.host
	snap.Samples = snap.Samples[1:]
	oneSample.host = &snap
	if line, _ := oneSample.vitalsLine(160); !strings.Contains(StripANSI(line), "cpu —") {
		t.Fatalf("one sample: %q", StripANSI(line))
	}
	// Quotas not read: the provider parts say so.
	notRead := vitalsFixture(th)
	notRead.vitals.read = false
	if line, _ := notRead.vitalsLine(160); !strings.Contains(StripANSI(line), "✳ claude …") {
		t.Fatalf("not read: %q", StripANSI(line))
	}
	// A quota whose last report is too old reads limit unknown.
	staleClaude := vitalsFixture(th)
	staleClaude.vitals.quotas[0] = usage.Quota{Provider: "claude", Reason: usage.ReasonStale}
	if line, _ := staleClaude.vitalsLine(160); !strings.Contains(StripANSI(line), "✳ claude limit unknown") {
		t.Fatalf("reason stale: %q", StripANSI(line))
	}
	// A stale-but-shown Known quota is marked old.
	staleShown := vitalsFixture(th)
	staleShown.vitals.quotas[0] = usage.Quota{Provider: "claude", Known: true, Stale: true, AsOf: vitalsNow.Add(-3 * time.Hour), Windows: []usage.Window{{Label: "wk", LeftPercent: 58, ResetsAt: vitalsNow.Add(98 * time.Hour)}}}
	if line, _ := staleShown.vitalsLine(160); !strings.Contains(StripANSI(line), "✳ claude ▓▓░ ~58% left ·4d (3h old)") {
		t.Fatalf("stale shown: %q", StripANSI(line))
	}
	// A low disk survives the drop step even at the narrow width.
	low := vitalsFixture(th)
	low.host = vitalsHost(true)
	if line, _ := low.vitalsLine(56); !strings.Contains(StripANSI(line), "disk") {
		t.Fatalf("low disk dropped at w=56: %q", StripANSI(line))
	}
}

func TestVitalsASCII(t *testing.T) {
	v := vitalsFixture(NewTheme(ThemeNoColor, false, false))
	line := v.render(layout.Pane{ID: layout.Vitals, Rect: layout.Rect{W: 80, H: 1}})[0]
	want := " cl 58% left . cx 88% left" + strings.Repeat(" ", 7) + "cpu 23% . 61C . ram 48% . disk 71% . 3 running "
	if got := StripANSI(line); got != want {
		t.Errorf("ascii:\n got %q\nwant %q", got, want)
	}
}

func TestVitalsOnEveryView(t *testing.T) {
	for _, c := range []struct {
		name string
		open func(w *Workspace)
	}{
		{"host", func(w *Workspace) { w.closeBodyViews(); w.hostView.Store(true) }},
		{"memory", func(w *Workspace) { w.openMemoryView() }},
		{"providers", func(w *Workspace) { w.openProviderView() }},
		{"verify", func(w *Workspace) { w.openVerifyView() }},
		{"security", func(w *Workspace) { w.openSecurityView() }},
		{"plan", func(w *Workspace) { w.openPlanView() }},
		{"arch", func(w *Workspace) { w.openArchView() }},
	} {
		t.Run(c.name, func(t *testing.T) {
			w, _ := boardWorkspace(t)
			c.open(w)
			lines, _, _ := w.frameLines(120, 40)
			if row0 := StripANSI(lines[0]); !strings.Contains(row0, "running") {
				t.Fatalf("row 0 is not the vitals row: %q", row0)
			}
			if row1 := StripANSI(lines[1]); !strings.HasPrefix(row1, " Marshal") {
				t.Fatalf("row 1 is not the header: %q", row1)
			}
		})
	}
	t.Run("empty", func(t *testing.T) {
		w := NewWorkspace(nil, "project", "session")
		w.screen = NewScreen(nil)
		w.homeBoard = true
		lines, _, _ := w.frameLines(120, 40)
		if row0 := StripANSI(lines[0]); !strings.Contains(row0, "running") {
			t.Fatalf("empty row 0 is not the vitals row: %q", row0)
		}
		if row1 := StripANSI(lines[1]); !strings.HasPrefix(row1, " Marshal") {
			t.Fatalf("empty row 1 is not the header: %q", row1)
		}
	})
}

func TestVitalsOnNonBoardScreens(t *testing.T) {
	// The diff viewer.
	wd, _ := boardWorkspace(t)
	wd.diffViewer = NewDiffViewer(nil, "")
	wd.diffViewer.OpenRaw("task changes", "diff --git a/a b/a\n--- a/a\n+++ b/a\n@@ -1 +1 @@\n-old\n+new\n")
	lines, cursor, _ := wd.frameLinesFull(120, 40)
	if len(lines) != 40 || !strings.Contains(StripANSI(lines[0]), "running") {
		t.Fatalf("diff viewer row 0 is not the vitals row: %q (%d lines)", StripANSI(lines[0]), len(lines))
	}
	if cursor < 1 {
		t.Fatalf("diff viewer cursor %d did not move for the vitals row", cursor)
	}
	// The classic screen.
	wc, _ := boardWorkspace(t)
	wc.setBoardView(true)
	lines, cursor, _ = wc.frameLinesFull(120, 40)
	if len(lines) != 40 || !strings.Contains(StripANSI(lines[0]), "running") {
		t.Fatalf("classic row 0 is not the vitals row: %q", StripANSI(lines[0]))
	}
	if cursor < 1 {
		t.Fatalf("classic cursor %d did not move for the vitals row", cursor)
	}
}

func TestVitalsHits(t *testing.T) {
	var hits []boardHit
	v := vitalsFixture(NewTheme(ThemeNoColor, true, false))
	v.hits = &hits
	v.providers = true // so a provider span records its hit
	v.renderLines(layout.Pane{ID: layout.Vitals, Rect: layout.Rect{X: 0, Y: 0, W: 160, H: 1}})
	want := map[string]bool{"health": false, "host": false, "lanes": false}
	for _, h := range hits {
		if h.kind != hitChip || h.row != 0 {
			t.Fatalf("unexpected hit %+v", h)
		}
		if _, ok := want[h.id]; ok {
			want[h.id] = true
		}
	}
	for id, seen := range want {
		if !seen {
			t.Errorf("no hitChip %q on the vitals row", id)
		}
	}
}
