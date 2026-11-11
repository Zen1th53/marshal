package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/nativehistory"
	"github.com/Zen1th53/marshal/internal/usage"
)

// usageRefreshEvery is how often the vitals row's provider quota is read
// again while a TUI screen is on screen.
const usageRefreshEvery = 30 * time.Second

// usageState is the last provider quota read for the vitals row. It holds
// only what the providers wrote locally; nothing here is estimated.
type usageState struct {
	mu     sync.Mutex
	read   bool
	quotas []usage.Quota
	// readFn and every replace the reader and the refresh interval in
	// tests; nil and zero mean usage.Read and usageRefreshEvery.
	readFn func(time.Time) []usage.Quota
	every  time.Duration
}

// startUsage reads the providers' quota every usageRefreshEvery while a TUI
// screen is up, until ctx is done. A read never runs on the render path.
func (w *Workspace) startUsage(ctx context.Context) {
	w.usage.mu.Lock()
	read, every := w.usage.readFn, w.usage.every
	w.usage.mu.Unlock()
	if read == nil {
		read = w.readUsage
	}
	if every <= 0 {
		every = usageRefreshEvery
	}
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			if w.screenShown() {
				w.storeUsage(read(time.Now()))
				// The chat's title carries its provider's limit; a reading, or
				// a reset countdown that moved, reaches it within one read.
				if w.dock.docked.Load() {
					w.kickDock()
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

// readUsage is usage.Read with Codex's sessions directory; an unresolved
// directory leaves Codex unknown.
func (w *Workspace) readUsage(now time.Time) []usage.Quota {
	root := ""
	if w.runtime != nil {
		root = w.runtime.ProjectRoot()
	}
	dir, err := nativehistory.HistoryDir(nativehistory.Codex, root)
	if err != nil {
		dir = ""
	}
	src := usage.Sources{CodexDir: dir, ClaudeFile: usage.ClaudeConfigFile()}
	return usage.Read(now, src)
}

// usageQuotas is the last reading, copied; ok is false before the first.
func (w *Workspace) usageQuotas() (qs []usage.Quota, ok bool) {
	w.usage.mu.Lock()
	defer w.usage.mu.Unlock()
	if !w.usage.read {
		return nil, false
	}
	return slices.Clone(w.usage.quotas), true
}

// storeUsage caches a reading and repaints the screen when the figure the
// vitals row shows changed.
func (w *Workspace) storeUsage(qs []usage.Quota) {
	now := time.Now()
	after := usageKey(qs, now)
	w.usage.mu.Lock()
	before := ""
	if w.usage.read {
		before = usageKey(w.usage.quotas, now)
	}
	w.usage.quotas, w.usage.read = qs, true
	w.usage.mu.Unlock()
	if after != before {
		w.requestRepaint()
	}
}

// usageKey is each quota's provider, the figure the vitals row would show
// (or its reason when unknown) and whether it is stale, so a reading that
// changes nothing the row shows does not repaint.
func usageKey(qs []usage.Quota, now time.Time) string {
	var b strings.Builder
	for _, q := range qs {
		fig, _, ok := limitFigure(q, now)
		if !ok {
			fig = q.Reason
		}
		fmt.Fprintf(&b, "%s|%s|%v;", q.Provider, fig, q.Stale)
	}
	return b.String()
}

// usageLeft is the percent a window has left. It is what the shown used
// figure leaves, so the two always add to 100 wherever both appear (picker,
// quota card, launch scan, vitals row).
func usageLeft(win usage.Window) int {
	return 100 - win.UsedShown()
}
