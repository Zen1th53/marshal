package tui

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// StartWithNativeOpenCode opens OpenCode after MARSHAL owns the terminal.
func (w *Workspace) StartWithNativeOpenCode(args []string) {
	w.StartWithNativeCodex(args)
	w.nativeStartupProvider = "opencode"
}

func openCodeUsesModelPreference(args []string) bool {
	for _, arg := range args {
		if arg == "-m" || arg == "--model" || strings.HasPrefix(arg, "--model=") {
			return false
		}
	}
	return len(args) == 0 || args[0] == "--continue" || args[0] == "--session" || args[0] == "--fork"
}

type openCodeSession struct {
	ID        string `json:"id"`
	Updated   int64  `json:"updated"`
	Directory string `json:"directory"`
}

func newOpenCodeHistoryWatch(binary, root string) *nativeHistoryWatch {
	watch := newNativeHistoryWatch("", root)
	watch.captureTools = true
	// The store is read while the session runs; the export below is the
	// fallback for a store whose shape has moved. Both are set, and sync picks
	// between them.
	if dbPath, err := openCodeDBPath(); err == nil {
		watch.openCodeDB = dbPath
	}
	watch.openCodeRun = func(args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.WaitDelay = 100 * time.Millisecond
		cmd.Dir = root
		out, err := cmd.Output()
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				return nil, fmt.Errorf("opencode %v: %s", args, exitErr.Stderr)
			}
			return nil, err
		}
		return out, nil
	}
	return watch
}

func (w *nativeHistoryWatch) primeOpenCode() error {
	capture, err := w.sharedCapture()
	if err != nil {
		return err
	}
	err = capture.Baseline(context.Background())
	w.seen = capture.SeenIndex()
	w.indexSaved, w.indexDirty = capture.IndexState()
	return err
}
