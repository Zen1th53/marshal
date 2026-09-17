package tui

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/importer"
)

func TestNativeOpenCodeHistoryImportsVisibleExportFields(t *testing.T) {
	root := t.TempDir()
	watch := newNativeHistoryWatch("", root)
	watch.captureTools = true
	watch.indexPath = filepath.Join(root, ".marshal", "opencode", "history-index.json")
	commands := make([]string, 0, 2)
	watch.openCodeRun = func(args ...string) ([]byte, error) {
		commands = append(commands, strings.Join(args, " "))
		if args[0] == "session" {
			return json.Marshal([]openCodeSession{{ID: "ses-1", Updated: 7, Directory: root}})
		}
		return []byte(`{"info":{"id":"ses-1","directory":"` + root + `","time":{"created":1787205973846}},"messages":[{"info":{"sessionID":"ses-1","role":"user","time":{"created":1787205973872}},"parts":[{"type":"text","text":"visible request"}]},{"info":{"sessionID":"ses-1","role":"assistant","time":{"created":1787205973887}},"parts":[{"type":"reasoning","text":"private"},{"type":"text","text":"visible answer"}]}]}`), nil
	}
	var captured []importer.Message
	watch.consume = func(tr importer.SessionTranscript) error {
		captured = append(captured, tr.Messages...)
		return nil
	}
	if err := watch.sync(); err != nil {
		t.Fatal(err)
	}
	if len(captured) != 2 || captured[0].Content != "visible request" || captured[1].Content != "visible answer" {
		t.Fatalf("captured = %+v", captured)
	}
	if len(commands) != 2 || commands[1] != "export ses-1" {
		t.Fatalf("commands = %q", commands)
	}
	commands = nil
	if err := watch.sync(); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 1 {
		t.Fatalf("unchanged session was exported again: %q", commands)
	}
}

func TestNativeOpenCodeRequiresTerminal(t *testing.T) {
	_, ws, ctx := newControlWorkspace(t)
	ws.terminal = NewTerminal(nil, nil)
	for _, command := range []string{"/opencode new", "/opencode continue", "/opencode resume", "/opencode fork", "/opencode cli --help"} {
		if _, err := ws.ExecuteCommand(ctx, command); err == nil {
			t.Fatalf("accepted noninteractive launch %s", command)
		}
	}
}

func TestNativeOpenCodeModelPreference(t *testing.T) {
	if !openCodeUsesModelPreference([]string{"--continue"}) {
		t.Fatal("lost selected model")
	}
	if openCodeUsesModelPreference([]string{"--session", "ses-1", "--model=openai/gpt-5"}) {
		t.Fatal("overrode explicit model")
	}
}
