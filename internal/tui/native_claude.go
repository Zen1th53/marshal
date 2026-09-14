package tui

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Zen1th53/marshal/internal/memory/importer"
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

// Claude histories may begin with file snapshots rather than session metadata.
// Decode complete entries and take identity from the first conversation entry.
func (w *nativeHistoryWatch) syncClaudeFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	var tr importer.SessionTranscript
	for {
		line, err := r.ReadSlice('\n')
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		item, err := (importer.ClaudeJSONLAdapter{}).Decode(line)
		if err != nil {
			return err
		}
		if item.SessionID == "" {
			continue
		}
		if tr.SessionID == "" {
			cwd := item.CWD
			if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
				cwd = resolved
			}
			if filepath.Clean(cwd) != w.root {
				return nil
			}
			tr = item
		} else {
			if item.SessionID != tr.SessionID {
				return errors.New("mixed Claude session IDs")
			}
			// Claude records cwd on every entry and it moves as the agent works
			// inside subdirectories of the project. That is ordinary, not
			// corruption, so it must not abort capture: this check previously
			// discarded the in-flight batch and, because sync leaves the file
			// unseen on error, re-failed on every poll. On this project's own
			// history that lost every message of two large sessions.
			//
			// The session identity above is the real binding, and the first
			// entry already proved the session belongs to this root. A path
			// below that root stays in scope; anything outside it does not.
			if item.CWD != "" && !claudeEntryWithinRoot(item.CWD, w.root) {
				return errors.New("Claude entry outside the project root")
			}
			tr.Messages = append(tr.Messages, item.Messages...)
		}
		if len(tr.Messages) >= 64 {
			if err := w.consume(tr); err != nil {
				return err
			}
			tr.Messages = nil
		}
	}
	if len(tr.Messages) != 0 {
		return w.consume(tr)
	}
	return nil
}

// claudeEntryWithinRoot reports whether a per-entry cwd still belongs to the
// project. Claude moves cwd into subdirectories during a session, so
// containment rather than equality is the correct test. Symlinks are resolved
// where possible so /tmp and its real target compare equal.
func claudeEntryWithinRoot(cwd, root string) bool {
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}
	cwd = filepath.Clean(cwd)
	root = filepath.Clean(root)
	if cwd == root {
		return true
	}
	relative, err := filepath.Rel(root, cwd)
	if err != nil {
		return false
	}
	// ".." anywhere in the relative path means the entry escaped the root.
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
