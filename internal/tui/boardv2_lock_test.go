package tui

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// The home screen's collectors read the workspace through its own locked
// accessors (GetUIState, marshalPanel), so a paint must run with w.mu not
// held: a read lock taken twice deadlocks as soon as a writer waits between
// the two. A writer that takes w.mu without pause makes that happen on the
// first frame painted under the lock; -race checks what the paint reads.
func TestHomePaintRunsWithoutTheWorkspaceLock(t *testing.T) {
	// Closed only after the frames came back: a deadlocked paint keeps
	// w.mu, and Close would wait on it too.
	w := emptyBoardWorkspace(t)
	w.endOpening()

	stop := make(chan struct{})
	var writer sync.WaitGroup
	writer.Add(1)
	go func() {
		defer writer.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			w.mu.Lock()
			w.unreadNew++
			w.mu.Unlock()
		}
	}()

	done := make(chan string, 1)
	go func() {
		var text string
		for range 100 {
			_ = w.boardV2()
			lines, _, _ := w.frameLines(120, 40)
			text = StripANSI(strings.Join(lines, "\n"))
			w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 'x'})
		}
		done <- text
	}()
	var text string
	select {
	case text = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("a Board v2 paint or key waited on w.mu: deadlock")
	}
	close(stop)
	writer.Wait()
	w.Close()
	if !strings.Contains(text, "From you:") {
		t.Fatalf("the frames were not the home screen's:\n%s", text)
	}
}
