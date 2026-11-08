//go:build !governed

package worker

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/hostgit"
)

type fileStamp struct {
	size     int64
	modified int64
}
type workerStallWatch struct {
	mu               sync.Mutex
	interval, window time.Duration
	worktree         string
	last             time.Time
	files            map[string]fileStamp
}

func newWorkerStallWatch(command adapter.Command) *workerStallWatch {
	if command.StallWindow <= 0 || command.StallWorktree == "" {
		return nil
	}
	interval := command.StallScanInterval
	if interval <= 0 {
		interval = time.Minute
	}
	w := &workerStallWatch{interval: interval, window: command.StallWindow, worktree: command.StallWorktree, last: time.Now()}
	w.files, _ = w.scan()
	return w
}

func (w *workerStallWatch) output(p []byte) {
	if w == nil || len(p) == 0 {
		return
	}
	w.mu.Lock()
	w.last = time.Now()
	w.mu.Unlock()
}

func (w *workerStallWatch) stalled(now time.Time) bool {
	if w == nil {
		return false
	}
	files, err := w.scan()
	w.mu.Lock()
	defer w.mu.Unlock()
	if err != nil {
		w.last = now
		return false
	} // A failed scan cannot prove inactivity.
	if !sameStamps(w.files, files) {
		w.files, w.last = files, now
		return false
	}
	return now.Sub(w.last) >= w.window
}

func sameStamps(a, b map[string]fileStamp) bool {
	if len(a) != len(b) {
		return false
	}
	for path, stamp := range a {
		if b[path] != stamp {
			return false
		}
	}
	return true
}

func (w *workerStallWatch) scan() (map[string]fileStamp, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd, err := hostgit.Command(ctx, w.worktree, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	files := make(map[string]fileStamp)
	for _, name := range bytes.Split(out, []byte{0}) {
		if len(name) == 0 {
			continue
		}
		path := string(name)
		if !filepath.IsLocal(path) {
			continue
		}
		info, err := os.Lstat(filepath.Join(w.worktree, path))
		if os.IsNotExist(err) {
			files[path] = fileStamp{size: -1}
			continue
		}
		if err != nil {
			return nil, err
		}
		files[path] = fileStamp{info.Size(), info.ModTime().UnixNano()}
	}
	return files, nil
}
