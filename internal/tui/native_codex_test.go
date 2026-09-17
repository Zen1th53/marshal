package tui

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/importer"
)

func TestNativeArgsPreserveQuotesWithoutShellExpansion(t *testing.T) {
	got, err := nativeArgs(`-i "a b.png" -c 'model="some-model"' -- 'fix $(touch nope); please' ""`)
	want := []string{"-i", "a b.png", "-c", `model="some-model"`, "--", "fix $(touch nope); please", ""}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("argv=%q err=%v", got, err)
	}
	for _, bad := range []string{`"unfinished`, `abc\`} {
		if _, err := nativeArgs(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestNativeSyncErrorsAreDeduplicated(t *testing.T) {
	first := errors.New("secret rejected")
	got := joinNativeSyncError(first, errors.New("secret rejected"))
	if got.Error() != "secret rejected" {
		t.Fatalf("duplicate error was repeated: %q", got)
	}
	got = joinNativeSyncError(got, errors.New("partial export"))
	if got.Error() != "secret rejected\npartial export" {
		t.Fatalf("distinct error was lost: %q", got)
	}
}

func nativeTestHistory(t *testing.T, root, id string) []byte {
	t.Helper()
	meta, _ := json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]string{"id": id, "cwd": root}})
	return append(meta, []byte(`
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fix the bug"}]}}
{"type":"response_item","payload":{"type":"reasoning","summary":[{"text":"private"}]}}
{"type":"response_item","payload":{"type":"message","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"working"}]}}
{"type":"response_item","payload":{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"fixed and tested"}]}}
`)...)
}

func TestNativeHistoryProjectIsolationPartialWritesAndRetry(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")
	data := nativeTestHistory(t, root, "thread-1")
	if err := os.WriteFile(path, append(append([]byte(nil), data...), []byte(`{"type":`)...), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other.jsonl"), nativeTestHistory(t, t.TempDir(), "other"), 0600); err != nil {
		t.Fatal(err)
	}
	w := newNativeHistoryWatch(dir, root)
	calls := 0
	w.consume = func(tr importer.SessionTranscript) error {
		calls++
		if tr.SessionID != "thread-1" || len(tr.Messages) != 2 {
			t.Fatalf("unexpected transcript: %+v", tr)
		}
		if calls == 1 {
			return errors.New("store unavailable")
		}
		return nil
	}
	if err := w.sync(); err == nil {
		t.Fatal("lost import failure")
	}
	if err := w.sync(); err != nil {
		t.Fatal(err)
	}
	if err := w.sync(); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("unchanged history imported again: %d", calls)
	}
	if err := os.WriteFile(path, append(data, []byte("{\"type\":\"turn_complete\"}\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	if err := w.sync(); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("completed tail was missed: %d", calls)
	}
}

func TestNativeCodexRejectsNonTerminal(t *testing.T) {
	_, ws, ctx := newControlWorkspace(t)
	ws.terminal = NewTerminal(nil, nil)
	if _, err := ws.ExecuteCommand(ctx, "/codex cli --help"); err == nil {
		t.Fatal("interactive launch accepted without a terminal")
	}
}

func TestNativeHistoryIndexSurvivesRestart(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")
	if err := os.WriteFile(path, nativeTestHistory(t, root, "saved"), 0600); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(root, ".marshal", "codex", "history-index.json")
	w := newNativeHistoryWatch(dir, root)
	w.indexPath = index
	w.consume = func(importer.SessionTranscript) error { return nil }
	if err := w.sync(); err != nil {
		t.Fatal(err)
	}
	restarted := newNativeHistoryWatch(dir, root)
	restarted.indexPath = index
	if err := restarted.loadIndex(); err != nil {
		t.Fatal(err)
	}
	restarted.consume = func(importer.SessionTranscript) error {
		t.Fatal("unchanged session replayed after restart")
		return nil
	}
	if err := restarted.sync(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(index)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("index permissions: %v, %v", info, err)
	}
}

func TestNativeHistorySkipsOversizedEventAndKeepsLaterMessages(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	meta, _ := json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]string{"id": "large", "cwd": root}})
	valid := `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"after large tool output"}]}}`
	data := append(meta, '\n')
	data = append(data, strings.Repeat("x", (1<<20)+100)...)
	data = append(data, '\n')
	data = append(data, valid...)
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(dir, "rollout.jsonl"), data, 0600); err != nil {
		t.Fatal(err)
	}
	w := newNativeHistoryWatch(dir, root)
	w.consume = func(tr importer.SessionTranscript) error {
		if len(tr.Messages) != 1 || tr.Messages[0].Content != "after large tool output" {
			t.Fatalf("messages = %+v", tr.Messages)
		}
		return nil
	}
	if err := w.sync(); err != nil {
		t.Fatal(err)
	}
}

func TestNativeModelPreferenceRespectsExplicitSelection(t *testing.T) {
	for _, args := range [][]string{nil, {"resume", "--last"}, {"--", "fix it"}} {
		if !nativeUsesModelPreference(args) {
			t.Fatalf("preference not used for %q", args)
		}
	}
	for _, args := range [][]string{{"resume", "--last", "-m", "explicit"}, {"fork", "id", "--model=explicit"}, {"mcp", "list"}, {"--profile", "work"}} {
		if nativeUsesModelPreference(args) {
			t.Fatalf("preference overrides explicit args %q", args)
		}
	}
}
