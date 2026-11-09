package tui

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/nativehistory"
	"github.com/Zen1th53/marshal/internal/usage"
)

// usageRefreshEvery is how often the header's provider quota is read again
// while the Board is on screen.
const usageRefreshEvery = 30 * time.Second

// usageState is the last provider quota read for the Board header. It holds
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

// startUsage reads the providers' quota every usageRefreshEvery while the
// Board is visible, until ctx is done. A read never runs on the render path.
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
			if w.boardVisible() {
				w.storeUsage(read(time.Now()))
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
	return usage.Read(now, dir)
}

// storeUsage caches a reading and repaints the Board when the header text
// it gives changed.
func (w *Workspace) storeUsage(qs []usage.Quota) {
	before, _ := w.usageSegment(0)
	w.usage.mu.Lock()
	w.usage.quotas, w.usage.read = qs, true
	w.usage.mu.Unlock()
	if after, _ := w.usageSegment(0); after != before {
		w.requestRepaint()
	}
}

// usageSegment is the header's quota part, wide and narrow:
//
//	codex 5h 82% · wk 49% left · claude unknown · opencode unknown · agy unknown
//	cx 82/49 · cl ? · oc ? · ag ?
//
// Both are empty until the first reading. The header drops the part first
// when it does not fit, so width does not change it.
func (w *Workspace) usageSegment(width int) (wide, narrow string) {
	w.usage.mu.Lock()
	qs, read := w.usage.quotas, w.usage.read
	w.usage.mu.Unlock()
	if !read {
		return "", ""
	}
	return usageWide(qs), usageNarrow(qs)
}

var usageShort = map[string]string{"codex": "cx", "claude": "cl", "opencode": "oc", "agy": "ag"}

func usageWide(qs []usage.Quota) string {
	parts := make([]string, 0, len(qs))
	for _, q := range qs {
		if !q.Known {
			parts = append(parts, q.Provider+" unknown")
			continue
		}
		ws := make([]string, len(q.Windows))
		for i, win := range q.Windows {
			if win.Known() {
				ws[i] = fmt.Sprintf("%s %d%%", win.Label, usageLeft(win))
			} else {
				ws[i] = win.Label + " unknown"
			}
		}
		parts = append(parts, q.Provider+" "+strings.Join(ws, " · ")+" left")
	}
	return strings.Join(parts, " · ")
}

func usageNarrow(qs []usage.Quota) string {
	parts := make([]string, 0, len(qs))
	for _, q := range qs {
		name := usageShort[q.Provider]
		if name == "" {
			name = q.Provider
		}
		if !q.Known {
			parts = append(parts, name+" ?")
			continue
		}
		ws := make([]string, len(q.Windows))
		for i, win := range q.Windows {
			if win.Known() {
				ws[i] = fmt.Sprint(usageLeft(win))
			} else {
				ws[i] = "?"
			}
		}
		parts = append(parts, name+" "+strings.Join(ws, "/"))
	}
	return strings.Join(parts, " · ")
}

// usageLeft rounds the percent left down, so the header never shows more
// quota than the provider reported.
func usageLeft(win usage.Window) int {
	return int(math.Floor(win.LeftPercent))
}
