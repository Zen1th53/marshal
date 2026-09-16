package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

// Claude writes cwd on every entry and moves it as the agent works inside
// subdirectories. Treating that as "mixed project directories" aborted capture
// and discarded the in-flight batch; on real histories from this project two
// large sessions lost every message. A subdirectory is in scope; only a path
// outside the root is not.
func TestNativeClaudeCapturesEntriesFromProjectSubdirectories(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	sub := filepath.Join(root, "internal", "web")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}

	entry := func(role, cwd string) []byte {
		payload := map[string]any{
			"type": role, "sessionId": "claude-subdir", "cwd": cwd,
			"timestamp": "2026-09-14T00:00:00Z",
			"message": map[string]any{"role": role, "content": []map[string]string{
				{"type": "text", "text": role + " from " + cwd},
			}},
		}
		line, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		return append(line, '\n')
	}

	var data []byte
	data = append(data, entry("user", root)...)
	data = append(data, entry("assistant", sub)...)
	data = append(data, entry("user", sub)...)
	if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	watch := newNativeHistoryWatch(dir, root)
	watch.claude = true
	captured := 0
	watch.consume = func(tr importer.SessionTranscript) error {
		captured += len(tr.Messages)
		return nil
	}
	if err := watch.syncClaudeFile(filepath.Join(dir, "session.jsonl")); err != nil {
		t.Fatalf("subdirectory entries must not abort capture: %v", err)
	}
	if captured != 3 {
		t.Fatalf("captured %d messages, want all 3 including the subdirectory entries", captured)
	}
}

// An entry genuinely outside the project root is still refused, so the relaxed
// check above cannot pull another project's conversation into this memory.
func TestNativeClaudeRefusesEntriesOutsideProjectRoot(t *testing.T) {
	dir, root, foreign := t.TempDir(), t.TempDir(), t.TempDir()

	entry := func(cwd string) []byte {
		payload := map[string]any{
			"type": "user", "sessionId": "claude-foreign", "cwd": cwd,
			"timestamp": "2026-09-14T00:00:00Z",
			"message": map[string]any{"role": "user", "content": []map[string]string{
				{"type": "text", "text": "text from " + cwd},
			}},
		}
		line, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		return append(line, '\n')
	}

	data := append(entry(root), entry(foreign)...)
	if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	watch := newNativeHistoryWatch(dir, root)
	watch.claude = true
	watch.consume = func(importer.SessionTranscript) error { return nil }
	if err := watch.syncClaudeFile(filepath.Join(dir, "session.jsonl")); err == nil {
		t.Fatal("an entry outside the project root must be refused")
	}
}

// The watcher is the seam where tool capture is actually switched on, so the
// default-off path above and this opt-in path are both pinned.
func TestNativeClaudeWatchCapturesToolCalls(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	entry := map[string]any{
		"type": "assistant", "sessionId": "tool-session", "cwd": root,
		"timestamp": "2026-09-14T00:00:00Z",
		"message": map[string]any{"role": "assistant", "content": []map[string]any{
			{"type": "text", "text": "editing"},
			{"type": "thinking", "thinking": "private reasoning"},
			{"type": "tool_use", "name": "Write", "input": map[string]string{
				"file_path": "main.go", "content": "package main",
			}},
		}},
	}
	line, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), append(line, '\n'), 0600); err != nil {
		t.Fatal(err)
	}

	w := newNativeHistoryWatch(dir, root)
	w.claude, w.captureTools = true, true
	var captured []importer.Message
	w.consume = func(tr importer.SessionTranscript) error {
		captured = append(captured, tr.Messages...)
		return nil
	}
	if err := w.sync(); err != nil {
		t.Fatal(err)
	}

	var toolUse *importer.Message
	for i := range captured {
		if captured[i].Kind == importer.MessageKindToolUse {
			toolUse = &captured[i]
		}
		if strings.Contains(captured[i].Content, "private reasoning") {
			t.Fatalf("thinking block reached memory: %q", captured[i].Content)
		}
	}
	if toolUse == nil {
		t.Fatalf("tool call was not captured: %+v", captured)
	}
	for _, want := range []string{"Write", "main.go", "+ package main"} {
		if !strings.Contains(toolUse.Content, want) {
			t.Errorf("captured call missing %q:\n%s", want, toolUse.Content)
		}
	}
}
