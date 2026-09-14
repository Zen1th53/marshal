package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/importer"
)

func nativeClaudeHistory(t *testing.T, root, id string) []byte {
	t.Helper()
	data := []byte("{\"type\":\"file-history-snapshot\"}\n")
	for _, role := range []string{"user", "assistant"} {
		entry := map[string]any{"type": role, "sessionId": id, "cwd": root, "timestamp": "2026-09-14T00:00:00Z",
			"message": map[string]any{"role": role, "content": []map[string]string{{"type": "text", "text": role + " visible text"}, {"type": "thinking", "text": "private reasoning"}, {"type": "tool_result", "text": "private output"}}}}
		line, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		data = append(append(data, line...), '\n')
	}
	return data
}

func TestNativeClaudeHistoryIsolationRecoveryAndFiltering(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), append(nativeClaudeHistory(t, root, "claude-session"), []byte(`{"type":`)...), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other.jsonl"), nativeClaudeHistory(t, t.TempDir(), "other-session"), 0600); err != nil {
		t.Fatal(err)
	}
	w := newNativeHistoryWatch(dir, root)
	w.claude = true
	w.indexPath = filepath.Join(root, ".marshal", "claude", "history-index.json")
	called := 0
	w.consume = func(tr importer.SessionTranscript) error {
		called++
		if tr.Provider != "claude" || tr.SessionID != "claude-session" || len(tr.Messages) != 2 {
			t.Fatalf("unexpected history: %+v", tr)
		}
		if tr.Messages[1].Content != "assistant visible text" {
			t.Fatalf("nonvisible payload imported: %+v", tr.Messages)
		}
		return nil
	}
	if err := w.sync(); err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Fatalf("import calls: %d", called)
	}
	restarted := newNativeHistoryWatch(dir, root)
	restarted.claude, restarted.indexPath = true, w.indexPath
	restarted.consume = func(importer.SessionTranscript) error { t.Fatal("duplicate import on restart"); return nil }
	if err := restarted.loadIndex(); err != nil {
		t.Fatal(err)
	}
	if err := restarted.sync(); err != nil {
		t.Fatal(err)
	}
}

func TestNativeClaudeRequiresTerminal(t *testing.T) {
	_, ws, ctx := newControlWorkspace(t)
	ws.terminal = NewTerminal(nil, nil)
	for _, command := range []string{"/claude cli --help", "/claude new", "/claude resume", "/claude continue", "/claude fork"} {
		if _, err := ws.ExecuteCommand(ctx, command); err == nil {
			t.Fatalf("accepted noninteractive launch %s", command)
		}
	}
}

func TestNativeClaudeModelPreference(t *testing.T) {
	if !claudeUsesModelPreference([]string{"--continue"}) {
		t.Fatal("lost selected model")
	}
	if claudeUsesModelPreference([]string{"--resume", "session", "--model=explicit"}) {
		t.Fatal("overrode explicit model")
	}
}
