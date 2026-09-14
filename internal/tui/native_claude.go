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
			if item.CWD != "" && filepath.Clean(item.CWD) != filepath.Clean(tr.CWD) {
				return errors.New("mixed Claude project directories")
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
