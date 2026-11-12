package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/store"
)

// The Marshal's own chat is not imported as a memory from "an earlier chat":
// on a brand-new project there is no earlier work, and the review would go to
// the same Marshal, which leaves it unanswered. Another session's history
// still becomes a candidate.
func TestMarshalOwnChatIsNotImportedAsMemory(t *testing.T) {
	for _, marshalChat := range []bool{true, false} {
		t.Run(fmt.Sprint("marshalChat=", marshalChat), func(t *testing.T) {
			w, rt := realControlWorkspace(t, "SESSION-own-chat-memory")
			root, dir := rt.ProjectRoot(), t.TempDir()
			watch := newNativeHistoryWatch(dir, root)
			w.guardHistoryWatch(watch, "codex")
			imported := 0
			watch.consume, _ = w.sessionMemoryConsume(t.Context(), rt, marshalChat, &imported)
			w.tmuxActiveWins = map[string]*activeTmuxAgent{"marshal-chat": {id: "marshal-chat", provider: "codex", role: "marshal-chat"}}
			if _, err := w.prepareChatHistoryWatch(root, watch, ""); err != nil {
				t.Fatal(err)
			}
			if Edition() != "Marshal" {
				// The governed edition reads the chat's history only under a
				// read grant, so it imports nothing here either way.
				t.Skip("the governed edition imports no history without a read grant")
			}
			data := fmt.Sprintf("{\"type\":\"session_meta\",\"payload\":{\"id\":\"chat\",\"cwd\":%q}}\n"+
				"{\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"user\",\"content\":[{\"type\":\"input_text\",\"text\":\"Add a divide function\"}]}}\n"+
				"{\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"assistant\",\"phase\":\"final_answer\",\"content\":[{\"type\":\"output_text\",\"text\":\"I am filling the requirements card.\"}]}}\n", root)
			if err := os.WriteFile(filepath.Join(dir, "current.jsonl"), []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := watch.sync(); err != nil {
				t.Fatal(err)
			}
			records, err := rt.Store().ListMemoryV2(t.Context(), store.MemoryQueryFilter{ProjectID: rt.ProjectID()})
			if err != nil {
				t.Fatal(err)
			}
			got := 0
			for _, rec := range records {
				if strings.HasPrefix(rec.ID, "MEM-IMPORT-") {
					got++
				}
			}
			if marshalChat && got != 0 {
				t.Fatalf("the Marshal's own chat was imported as %d memory candidate(s)", got)
			}
			if !marshalChat && got == 0 {
				t.Fatal("another session's history was not captured")
			}
			if marshalChat {
				for _, req := range w.permissions.queue.Take(false) {
					if req.Kind == "memory" {
						t.Fatalf("a memory review was asked for the Marshal's own chat: %+v", req)
					}
				}
			}
		})
	}
}
