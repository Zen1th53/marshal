package tui

import (
	"strings"
)

// unsentPaste is the Board's note that it pasted a message into the chat
// that the provider has not taken yet, and the status line it showed for
// that. When the paste is later seen submitted the status is replaced by an
// honest one instead of staying a false "not sent".
type unsentPaste struct {
	pane, text string
	status     string
	settle     func()
}

// noteUnsentPaste records the paste of text into pane as waiting, with the
// status line shown for it (empty until the caller sets it).
func (w *Workspace) noteUnsentPaste(pane, text, status string, settle func()) {
	w.unsentMu.Lock()
	w.unsent = &unsentPaste{pane: pane, text: text, status: status, settle: settle}
	w.unsentMu.Unlock()
}

// setUnsentStatus records the status line shown for the waiting paste.
func (w *Workspace) setUnsentStatus(status string) {
	w.unsentMu.Lock()
	if w.unsent != nil {
		w.unsent.status = status
	}
	w.unsentMu.Unlock()
}

// settleUnsentPaste is called when the paste recorded for pane is seen
// submitted (sent) or gone from the input without a trace of a turn. It
// replaces the "not sent" status when it is still showing; runSettle also
// completes what waited on the delivery (an ask card's receipt).
func (w *Workspace) settleUnsentPaste(pane string, sent, runSettle bool) {
	w.unsentMu.Lock()
	u := w.unsent
	if u == nil || u.pane != pane {
		w.unsentMu.Unlock()
		return
	}
	if runSettle {
		w.unsent = nil
	}
	w.unsentMu.Unlock()
	if u.status != "" {
		w.mu.RLock()
		showing := w.answer == u.status
		w.mu.RUnlock()
		if showing {
			if sent {
				w.memoryAnswer("Sent.", false)
			} else {
				w.memoryAnswer("The pasted message is no longer in the Marshal chat input; open the chat (F11) to see whether it was sent.", true)
			}
		}
	}
	if runSettle && sent && u.settle != nil {
		u.settle()
	}
}

// pasteLeftInput reports whether a composer that no longer holds our paste
// shows a turn that took it: the chat is busy, or its text is in the frame.
func pasteLeftInput(frame, text string) bool {
	if chatBusyNearPrompt(frame) {
		return true
	}
	snippet := strings.TrimSpace(strings.SplitN(strings.TrimSpace(text), "\n", 2)[0])
	if r := []rune(snippet); len(r) > 30 {
		snippet = string(r[:30])
	}
	return snippet != "" && strings.Contains(StripANSI(frame), snippet)
}
