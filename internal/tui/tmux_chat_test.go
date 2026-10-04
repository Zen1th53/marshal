package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/tmux"
)

func TestMarshalChatCapturesDurableConversationAndRestarts(t *testing.T) {
	w := realTmuxWorkspace(t)
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "sessions"), 0700); err != nil {
		t.Fatal(err)
	}
	old := `{"type":"session_meta","payload":{"id":"wrong-old-session","cwd":` + strconv.Quote(w.workDir) + `}}` + "\n"
	old += `{"type":"response_item","payload":{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"Old chat"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(home, "sessions", "0-old.jsonl"), []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	// A local provider fixture records a real history entry and accepts input.
	script := `#!/bin/sh
mkdir -p "$CODEX_HOME/sessions"
printf '{"type":"session_meta","payload":{"id":"chat-bound-id","cwd":"%s"}}\n' "$PWD" > "$CODEX_HOME/sessions/chat.jsonl"
printf '{"type":"response_item","payload":{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"Marshal ready"}]}}\n' >> "$CODEX_HOME/sessions/chat.jsonl"
echo CHAT_READY
while read line; do echo input:$line; done
`
	if err := os.WriteFile(filepath.Join(binDir, "codex"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	m := w.marshalSession()
	m.mu.Lock()
	m.runID = "run-held"
	m.mu.Unlock()
	w.tmuxMu.Lock()
	w.startMarshalChatLocked(context.Background(), w.workDir)
	a := w.tmuxActiveWins["marshal-chat"]
	w.tmuxMu.Unlock()
	defer a.cancel()
	if len(a.args) == 0 {
		t.Fatal("automatic chat has no Marshal role instructions")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		data, _ := os.ReadFile(filepath.Join(w.workDir, ".marshal", "tmux-chat.json"))
		var saved map[string]any
		json.Unmarshal(data, &saved)
		if saved["session_id"] == "chat-bound-id" {
			if saved["run_id"] != "run-held" {
				t.Fatal("automatic chat lost the stored Marshal run")
			}
			break
		}
		if time.Now().After(deadline) {
			out, _ := tmux.CapturePane(context.Background(), a.paneID)
			history, _ := os.ReadFile(filepath.Join(home, "sessions", "chat.jsonl"))
			t.Fatalf("conversation was not captured durably: session=%v pane=%q history=%q", saved["session_id"], out, history)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := tmux.RunCommand(context.Background(), "send-keys", "-t", a.paneID, "hello", "Enter"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	out, _ := tmux.CapturePane(context.Background(), a.paneID)
	if !strings.Contains(out, "input:hello") {
		t.Fatalf("chat input missing: %s", out)
	}
	// Use the captured binding, rather than injecting a synthetic ID in the pane.
	w.restartMarshalChat(context.Background(), a, w.workDir)
	w.tmuxMu.Lock()
	args := append([]string(nil), a.args...)
	id := a.sessionID
	w.tmuxMu.Unlock()
	if id != "chat-bound-id" || !strings.Contains(strings.Join(args, " "), "chat-bound-id") {
		t.Fatalf("restart lost conversation binding: %s %v", id, args)
	}
	w.StopAllWorkers(context.Background())
	panes, _ := tmux.ListPanes(context.Background(), w.tmuxSession)
	found := false
	for _, p := range panes {
		if p.PaneID == a.paneID {
			found = true
		}
	}
	if !found {
		t.Fatal("stop-all removed chat")
	}
}
