package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/nativehistory"
)

const antigravityBinary = "agy"

func (w *Workspace) StartWithNativeAntigravity(args []string) {
	w.StartWithNativeCodex(args)
	w.nativeStartupProvider = "antigravity"
}

func newAntigravityHistoryWatch(root string) (*nativeHistoryWatch, error) {
	dir, err := nativehistory.HistoryDir(nativehistory.Antigravity, root)
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	watch := newNativeHistoryWatch(dir, root)
	watch.antigravity = true
	watch.antigravitySummaries = filepath.Join(home, ".gemini", "antigravity-cli", "conversation_summaries.db")
	return watch, nil
}

func (w *nativeHistoryWatch) primeAntigravity() error {
	capture, err := w.sharedCapture()
	if err != nil {
		return err
	}
	err = capture.Baseline(context.Background())
	w.seen = capture.SeenIndex()
	w.indexSaved, w.indexDirty = capture.IndexState()
	return err
}

// prepareAntigravityDirectLaunch establishes the baseline and sets chatFile so
// the direct-TTY path captures the conversation launched by this run even if
// agy has not yet recorded it in conversation_summaries.db. Conversations
// confirmed for other projects remain skipped.
func (w *nativeHistoryWatch) prepareAntigravityDirectLaunch(args []string) error {
	if err := w.primeAntigravity(); err != nil {
		return err
	}
	existing := make(map[string]bool)
	entries, err := os.ReadDir(w.dir)
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".db") {
				existing[entry.Name()] = true
			}
		}
	}
	targetID := antigravityConversationIDFromArgs(args)
	if targetID == "" && hasAgyFlag(args, "-c", "--continue") {
		targetID = newestExistingAntigravityConversation(w.dir, existing)
	}
	var boundPath string
	var mu sync.Mutex

	w.chatFile = func(path string) bool {
		mu.Lock()
		defer mu.Unlock()

		if targetID != "" {
			id := strings.TrimSuffix(filepath.Base(path), ".db")
			return id == targetID
		}
		if boundPath != "" {
			return path == boundPath
		}
		if existing[filepath.Base(path)] {
			return false
		}
		candidate := findNewestAntigravityCandidate(w.dir, existing)
		if candidate != "" && path == candidate {
			boundPath = candidate
			return true
		}
		return false
	}
	return nil
}

func antigravityConversationIDFromArgs(args []string) string {
	for i, arg := range args {
		if arg == "--conversation" && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(arg, "--conversation=") {
			return strings.TrimPrefix(arg, "--conversation=")
		}
	}
	if len(args) >= 2 && strings.EqualFold(args[0], "resume") && !strings.HasPrefix(args[1], "-") {
		return args[1]
	}
	return ""
}

func hasAgyFlag(args []string, flags ...string) bool {
	for _, arg := range args {
		if arg == "--" {
			break
		}
		for _, flag := range flags {
			if arg == flag || (strings.HasPrefix(flag, "--") && strings.HasPrefix(arg, flag+"=")) {
				return true
			}
		}
	}
	return false
}

func newestExistingAntigravityConversation(dir string, existing map[string]bool) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var newestID string
	var newestMod time.Time
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".db") || !existing[name] {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if newestID == "" || info.ModTime().After(newestMod) || (info.ModTime().Equal(newestMod) && name > newestID+".db") {
			newestID = strings.TrimSuffix(name, ".db")
			newestMod = info.ModTime()
		}
	}
	return newestID
}

func findNewestAntigravityCandidate(dir string, existing map[string]bool) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var newestPath string
	var newestMod time.Time
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".db") {
			continue
		}
		if existing[name] {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if newestPath == "" || info.ModTime().After(newestMod) || (info.ModTime().Equal(newestMod) && name > filepath.Base(newestPath)) {
			newestPath = filepath.Join(dir, name)
			newestMod = info.ModTime()
		}
	}
	return newestPath
}

func splitWorkspaceURIs(value string) []string { return nativehistory.SplitWorkspaceURIs(value) }

func (w *nativeHistoryWatch) antigravityWorkspaceMatches(uris []string) bool {
	capture, err := w.sharedCapture()
	return err == nil && capture.WorkspaceMatches(uris)
}
