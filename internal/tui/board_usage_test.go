package tui

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

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

// TestUsageFromCodexFile reads a real rollout file through usage.Read and
// caches it, so the vitals row and the reader agree on what is known.
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
	qs, ok := usageWorkspace(usage.Read(usageNow, usage.Sources{CodexDir: dir})).usageQuotas()
	if !ok || len(qs) == 0 || qs[0].Provider != "codex" || !qs[0].Known {
		t.Fatalf("fresh file: %+v ok=%v", qs, ok)
	}
	if fig, pct, ok := limitFigure(qs[0], usageNow); !ok || pct != 49 || fig == "" {
		t.Fatalf("fresh codex figure: %q pct=%d ok=%v", fig, pct, ok)
	}
	// A day later the same file is stale: unknown, not the old percent.
	stale := usage.Read(usageNow.Add(24*time.Hour), usage.Sources{CodexDir: dir})
	if _, ok := stale[0].Binding(); ok {
		t.Fatalf("stale file still known: %+v", stale[0])
	}
}

func TestUsageRefreshWhileScreenShown(t *testing.T) {
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
	if qs, ok := w.usageQuotas(); !ok || len(qs) == 0 || qs[0].Provider != "codex" || !qs[0].Known {
		t.Fatalf("cached = %+v ok=%v", qs, ok)
	}
	cancel()
	time.Sleep(20 * time.Millisecond)
	stopped := reads.Load()
	time.Sleep(30 * time.Millisecond)
	if reads.Load() != stopped {
		t.Fatal("usage still read after ctx done")
	}
}

func TestUsageNoReadWhileNoScreen(t *testing.T) {
	// No screen: nothing is read and no reading is kept.
	w := &Workspace{}
	var reads atomic.Int32
	w.usage.every = 2 * time.Millisecond
	w.usage.readFn = func(time.Time) []usage.Quota { reads.Add(1); return usageFresh() }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.startUsage(ctx)
	time.Sleep(30 * time.Millisecond)
	if reads.Load() != 0 {
		t.Fatalf("read %d times while no screen", reads.Load())
	}
	if _, ok := w.usageQuotas(); ok {
		t.Fatal("no screen must keep no reading")
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
