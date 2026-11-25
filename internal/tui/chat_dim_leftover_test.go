package tui

import (
	"context"
	"testing"
)

// Claude often suggests, dimmed, the very words the Board just sent. A
// remembered paste of ours whose text equals that suggestion is not a
// leftover to submit: Enter on the suggestion would send words the owner did
// not choose this time. Both the leftover sweep and a card's second press
// must leave it alone and forget the stale record.
func TestDimSuggestionWithOurWordsIsNeverSubmitted(t *testing.T) {
	const text = "No fixed budget or deadline"
	for _, path := range []string{"leftover", "card"} {
		t.Run(path, func(t *testing.T) {
			ownPastes.mu.Lock()
			ownPastes.panes = map[string]ownPaste{}
			ownPastes.mu.Unlock()
			w := emptyBoardWorkspace(t)
			defer w.Close()
			f := &fakeClaudePane{suggestion: readFrame(t, "mac_frame_suggestion.txt")}
			w.chatRun = f.run
			rememberOwnPaste("%7", text, text)
			defer forgetOwnPaste("%7")
			if path == "leftover" {
				w.submitOwnLeftover(context.Background(), "%7", false)
				if _, still := ownPasteFor("%7"); still {
					t.Fatal("the stale record survived a frame showing only a suggestion")
				}
			} else {
				ctx, cancel := context.WithCancel(context.Background())
				cancel() // stop after the leftover check; no new paste is wanted here
				_, _ = w.typeIntoChatStrict(ctx, "%7", text)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.enters != 0 {
				t.Fatalf("Enter was sent %d times on a dim suggestion: %v", f.enters, f.cmds)
			}
		})
	}
}
