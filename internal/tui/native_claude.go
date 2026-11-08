package tui

import (
	"strings"
)

func (w *Workspace) StartWithNativeClaude(args []string) {
	w.StartWithNativeCodex(args)
	w.nativeStartupProvider = "claude"
}

func claudeUsesModelPreference(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "--model" || strings.HasPrefix(arg, "--model=") {
			return false
		}
	}
	return len(args) == 0 || args[0] == "--" || args[0] == "--continue" || args[0] == "--resume"
}

// syncClaudeFile remains as a thin test and compatibility adapter.
func (w *nativeHistoryWatch) syncClaudeFile(path string) error {
	capture, err := w.sharedCapture()
	if err != nil {
		return err
	}
	return capture.SyncFile(path)
}
