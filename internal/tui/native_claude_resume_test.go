package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/statepath"
)

// The Marshal chat's monitor syncs the chat's Claude history every second
// while the chat writes to it. Each sync builds a new Capture, so the place
// the last sync stopped must outlive it: otherwise every second decodes the
// whole history again (tens of megabytes for a long chat), which kept one
// core at 100% on an otherwise idle Board. A sync after an append must decode
// only the appended records, however long the history already is.
func TestLiveClaudeChatSyncDecodesOnlyAppendedRecords(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	path := filepath.Join(dir, "chat.jsonl")
	record := func(n int) []byte {
		line, err := json.Marshal(map[string]any{"type": "assistant", "sessionId": "chat", "cwd": root, "timestamp": "2026-09-14T00:00:00Z",
			"message": map[string]any{"role": "assistant", "content": []map[string]string{{"type": "text", "text": "answer"}}}})
		if err != nil {
			t.Fatal(err)
		}
		return append(line, '\n')
	}
	const history = 2000
	var data []byte
	for i := range history {
		data = append(data, record(i)...)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	w := newNativeHistoryWatch(dir, root)
	w.claude = true
	w.indexPath = filepath.Join(root, statepath.DirName, "claude", "history-index.json")
	// Records are handed on in batches; count them per sync.
	n := 0
	w.consume = func(tr importer.SessionTranscript) error {
		n += len(tr.Messages)
		return nil
	}
	if err := w.sync(); err != nil {
		t.Fatal(err)
	}
	if n != history {
		t.Fatalf("first sync decoded %d records, want %d", n, history)
	}
	// The chat answers ten times; the monitor syncs after each.
	for i := range 10 {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(record(history + i)); err != nil {
			t.Fatal(err)
		}
		f.Close()
		n = 0
		if err := w.sync(); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("sync after append %d decoded %d records, want 1: the history was read from the start again", i+1, n)
		}
	}
}
