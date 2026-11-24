package tui

import (
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/permission"
)

// chatBindingState is what the Board knows about its Marshal chat beyond the
// pane: restarts and rebinds, the requests it sent and how they were
// answered, and the delivery notes it already logged. Its mutex is a leaf:
// nothing else is locked while it is held.
type chatBindingState struct {
	mu        sync.Mutex
	restarts  int
	restartAt time.Time
	reboundAt time.Time
	resent    int
	answered  int
	requests  []chatRequestRecord // oldest first, at most chatRequestLimit
	// blocked is the delivery note logged for the current blockage ("" when
	// delivery is not blocked); repeats only update that note.
	blocked string
}

// chatRequestRecord is one request the Board sent to the Marshal chat.
type chatRequestRecord struct {
	id         string
	sent       time.Time
	what       string
	answer     string // allow, decline, pending, expired
	answeredAt time.Time
}

const chatRequestLimit = 20

// Delivery notes. Each is logged once per blockage, however often the
// delivery is retried.
const (
	chatQueuedOwnerInput = "Marshal decision queued: chat input is not empty or ready. Send or clear your unsent text in the chat pane to unblock delivery."
	chatQueuedUnsent     = "Marshal decision queued: the chat has not taken the pasted request yet. MARSHAL presses Enter for it whenever it can read the chat's input; if it stays there, open the chat (F11) and press Enter."
)

func (w *Workspace) chatRestarted() {
	c := &w.chatBinding
	c.mu.Lock()
	c.restarts++
	c.restartAt = time.Now()
	c.mu.Unlock()
}

func (w *Workspace) chatRebound(at time.Time, resent int) {
	c := &w.chatBinding
	c.mu.Lock()
	c.reboundAt = at
	c.resent += resent
	c.mu.Unlock()
}

// chatRequestSent records a delivered request. A re-delivery of the same ID
// keeps the first entry and its sent time.
func (w *Workspace) chatRequestSent(id string, req permission.Request, at time.Time) {
	c := &w.chatBinding
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.requests {
		if c.requests[i].id == id {
			return
		}
	}
	c.requests = append(c.requests, chatRequestRecord{id: id, sent: at, what: requestPlainWords(req), answer: "pending"})
	if len(c.requests) > chatRequestLimit {
		c.requests = c.requests[len(c.requests)-chatRequestLimit:]
	}
}

// chatRequestWhat is the plain words of the request the Board sent with
// id, "" when it sent none.
func (w *Workspace) chatRequestWhat(id string) string {
	c := &w.chatBinding
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.requests {
		if c.requests[i].id == id {
			return c.requests[i].what
		}
	}
	return ""
}

func (w *Workspace) chatRequestAnswered(id string, allow bool, at time.Time) {
	c := &w.chatBinding
	c.mu.Lock()
	defer c.mu.Unlock()
	c.answered++
	for i := range c.requests {
		if c.requests[i].id == id && c.requests[i].answer == "pending" {
			c.requests[i].answer = "decline"
			if allow {
				c.requests[i].answer = "allow"
			}
			c.requests[i].answeredAt = at
		}
	}
}

func (w *Workspace) chatRequestExpired(id string) {
	c := &w.chatBinding
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.requests {
		if c.requests[i].id == id && c.requests[i].answer == "pending" {
			c.requests[i].answer = "expired"
		}
	}
}

// noteChatOnce logs a delivery note once per blockage. While the same note
// stands, a repeat only moves the logged note's time forward instead of
// adding another Activity line every poll.
func (w *Workspace) noteChatOnce(text string) {
	c := &w.chatBinding
	c.mu.Lock()
	repeat := c.blocked == text
	c.blocked = text
	c.mu.Unlock()
	if !repeat {
		w.RecordActivity(text)
		return
	}
	w.touchNote(text)
}

// chatDeliveryUnblocked ends the current blockage: the next one is logged.
func (w *Workspace) chatDeliveryUnblocked() {
	c := &w.chatBinding
	c.mu.Lock()
	c.blocked = ""
	c.mu.Unlock()
}

// touchNote moves the newest note with text to the front with the current
// time; it adds nothing when no such note is kept.
func (w *Workspace) touchNote(text string) {
	text = strings.TrimSpace(hideMarshalProtocol(RedactContent(text, nil)))
	n := &w.notes
	n.mu.Lock()
	for i := range n.list {
		if n.list[i].Text == text && !n.list[i].Err {
			note := n.list[i]
			note.At = time.Now()
			copy(n.list[1:i+1], n.list[:i])
			n.list[0] = note
			break
		}
	}
	n.mu.Unlock()
	w.requestRepaint()
}

// requestPlainWords says what a request asks, for the owner.
func requestPlainWords(req permission.Request) string {
	object := strings.TrimSpace(req.Object)
	switch req.Kind {
	case "project-contract":
		return "apply architecture"
	case "memory":
		if strings.HasPrefix(object, "MEM-IMPORT-") {
			return "memory import"
		}
		return "keep memory"
	case "network":
		return "network " + object
	case "read":
		return "read " + object
	case "credential", "credential-bound":
		return "use " + object + " sign-in"
	case "marshal-command":
		switch {
		case object == "/marshal approve":
			return "approve plan"
		case object == "/marshal deliver":
			return "deliver run"
		case strings.HasPrefix(object, "/marshal approve-task "):
			return "approve task"
		case strings.HasPrefix(object, hostCapObject+" "):
			return "temporary lane cap " + lastField(object)
		case strings.HasPrefix(object, "/harness verify"):
			return "verify provider"
		case strings.HasPrefix(object, "/marshal setting parallel"), strings.HasPrefix(object, "/marshal parallel"):
			return "parallel limit " + lastField(object)
		}
		return strings.TrimPrefix(object, "/marshal ")
	}
	if object == "" {
		return req.Kind
	}
	return strings.TrimSpace(req.Kind + " " + object)
}

func lastField(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	return f[len(f)-1]
}
