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
	waitForTmuxOutput(t, a.paneID, "input:hello")
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

func TestRecoveredChatPreservesHistoryProvenance(t *testing.T) {
	for _, durableBaseline := range []bool{false, true} {
		t.Run(strconv.FormatBool(durableBaseline), func(t *testing.T) {
			w := realTmuxWorkspace(t)
			home := t.TempDir()
			t.Setenv("CODEX_HOME", home)
			dir := filepath.Join(home, "sessions")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			write := func(id string) {
				data := `{"type":"session_meta","payload":{"id":` + strconv.Quote(id) + `,"cwd":` + strconv.Quote(w.workDir) + `}}` + "\n"
				if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			write("old")
			binding := chatBinding{Provider: "codex", Binary: "/bin/true"}
			if durableBaseline {
				binding.HistoryBaseline = []string{"old"}
				write("new")
			}
			if err := saveChatBinding(w.workDir, binding); err != nil {
				t.Fatal(err)
			}
			panes, err := tmux.ListPanes(context.Background(), w.tmuxSession)
			if err != nil {
				t.Fatal(err)
			}
			a := &activeTmuxAgent{id: "marshal-chat", role: "marshal-chat", provider: "codex", paneID: panes[0].PaneID, window: "marshal"}
			if err := saveAgentRecord(w.workDir, w.tmuxSession, a); err != nil {
				t.Fatal(err)
			}
			w.tmuxMu.Lock()
			w.adoptSurvivingWorkersLocked(w.workDir)
			a = w.tmuxActiveWins["marshal-chat"]
			w.tmuxMu.Unlock()
			if a == nil {
				t.Fatal("chat was not adopted")
			}
			if got := loadChatBinding(w.workDir).HistoryBaseline; len(got) != 1 || got[0] != "old" {
				t.Fatalf("recovered provenance was not retained: %v", got)
			}
			defer a.cancel()
			if !durableBaseline {
				time.Sleep(1100 * time.Millisecond)
				if got := loadChatBinding(w.workDir).SessionID; got != "" {
					t.Fatalf("adopted historical conversation %q", got)
				}
				write("new")
			}
			deadline := time.Now().Add(3 * time.Second)
			for loadChatBinding(w.workDir).SessionID != "new" {
				if time.Now().After(deadline) {
					t.Fatalf("recovered binding = %#v", loadChatBinding(w.workDir))
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}
