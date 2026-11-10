package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/statepath"
)

type attnSnapshot struct {
	observed, needsYou, closed, stopped bool
	runID, stopNote                     string
	seen                                map[string]time.Time
}
type attnEvent struct{ kind, title, body, runID string }
type notifier interface {
	Bell() error
	Notify(context.Context, string, string) error
}
type attentionState struct {
	mu       sync.Mutex
	started  bool
	previous attnSnapshot
	now      func() time.Time
	notifier notifier
	failures map[string]bool
}

func (w *Workspace) attentionNow() attnSnapshot {
	w.mu.RLock()
	p := w.state.Marshal
	v := boardVisual{}
	if p != nil {
		v.board, v.note, v.stopped = p.Board, p.Note, p.Stopped
	}
	w.mu.RUnlock()
	v.gates = w.ownerGates(p)
	v.guard, _, _, _ = w.guardPaint()
	s, stopped := v.stopNotice()
	needs := v.ownerWaits()
	if stopped {
		needs = s.waiting
	}
	return attnSnapshot{observed: true, needsYou: needs, runID: v.board.Run.ID, closed: v.board.Run.State == marshal.Closed, stopped: stopped && !s.waiting, stopNote: s.reason}
}

// attentionEdges does not mutate either snapshot, including its dedup history.
func attentionEdges(prev, next attnSnapshot, now time.Time) []attnEvent {
	if !prev.observed || prev.runID != next.runID {
		return nil
	}
	var out []attnEvent
	add := func(kind, title, body string) {
		if at, ok := prev.seen[kind+":"+next.runID]; kind != "closed" && ok && now.Sub(at) < 30*time.Second {
			return
		}
		out = append(out, attnEvent{kind, title, body, next.runID})
	}
	if !prev.needsYou && next.needsYou {
		add("attention", "Marshal needs you", "Run "+next.runID+" needs your attention on the Board.")
	}
	if !prev.closed && next.closed {
		add("closed", "Run closed", "Run "+next.runID+" closed.")
	}
	if next.stopped && !next.closed && (!prev.stopped || prev.stopNote != next.stopNote) {
		add("stopped", "Run stopped: "+next.stopNote, "Run "+next.runID+": "+next.stopNote)
	}
	return out
}

func (w *Workspace) attentionPoll(ctx context.Context) {
	next := w.attentionNow()
	a := &w.attn
	a.mu.Lock()
	now := time.Now()
	if a.now != nil {
		now = a.now()
	}
	events := attentionEdges(a.previous, next, now)
	next.seen = make(map[string]time.Time)
	if a.previous.runID == next.runID {
		for key, at := range a.previous.seen {
			next.seen[key] = at
		}
	}
	for _, e := range events {
		next.seen[e.kind+":"+e.runID] = now
	}
	a.previous = next
	n := a.notifier
	if n == nil {
		n = &desktopNotifier{terminal: w.terminal}
		a.notifier = n
	}
	a.mu.Unlock()
	if !attentionEnabled(w.providerRoot()) {
		return
	}
	for _, e := range events {
		w.attentionFailure("bell", n.Bell()) // Poll runs on the input loop.
		go func(e attnEvent) { w.attentionFailure("desktop", n.Notify(ctx, e.title, e.body)) }(e)
	}
}

func (w *Workspace) attentionFailure(kind string, err error) {
	if err == nil {
		return
	}
	a := &w.attn
	a.mu.Lock()
	if a.failures == nil {
		a.failures = make(map[string]bool)
	}
	first := !a.failures[kind]
	a.failures[kind] = true
	a.mu.Unlock()
	if first {
		w.addNote(fmt.Sprintf("Notification %s failed: %v", kind, err), true)
	}
}

func (w *Workspace) startAttention(ctx context.Context) {
	a := &w.attn
	a.mu.Lock()
	if a.started {
		a.mu.Unlock()
		return
	}
	a.started = true
	a.mu.Unlock()
	go func() {
		if ctx.Err() != nil {
			return
		}
		if !w.postUI(ctx, func() { w.attentionPoll(ctx) }) {
			return
		}
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !w.postUI(ctx, func() { w.attentionPoll(ctx) }) {
					return
				}
			}
		}
	}()
}

type desktopNotifier struct {
	terminal *Terminal
	once     sync.Once
	path     string
}

func (n *desktopNotifier) Bell() error {
	if n.terminal != nil {
		return n.terminal.Bell()
	}
	return nil
}
func (n *desktopNotifier) Notify(ctx context.Context, title, body string) error {
	n.once.Do(func() { n.path, _ = exec.LookPath("notify-send") })
	if n.path == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, n.path, "-a", "MARSHAL", title, body).Run()
}

func attentionPath(root string) string { return filepath.Join(root, statepath.DirName, "notify") }
func attentionEnabled(root string) bool {
	if root == "" || statepath.CheckPath(root, attentionPath(root)) != nil {
		return true
	}
	data, err := os.ReadFile(attentionPath(root))
	return err != nil || strings.TrimSpace(string(data)) != "off"
}
func saveAttention(root, value string) error {
	if root == "" {
		return fmt.Errorf("no project is open")
	}
	path := attentionPath(root)
	if err := statepath.CheckPath(root, path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(value+"\n"), 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// notifyCommand turns attention alerts on or off and returns the reply the
// command line shows; the Board also records it as a note.
func (w *Workspace) notifyCommand(args []string) string {
	root := w.providerRoot()
	if len(args) > 1 || (len(args) == 1 && args[0] != "status" && args[0] != "on" && args[0] != "off") {
		msg := "Usage: /notify on|off|status"
		w.addNote(msg, true)
		return msg
	}
	if len(args) == 1 && args[0] != "status" {
		if err := saveAttention(root, args[0]); err != nil {
			msg := "Notifications: " + err.Error()
			w.addNote(msg, true)
			return msg
		}
	}
	status := "on"
	if !attentionEnabled(root) {
		status = "off"
	}
	msg := fmt.Sprintf("Notifications %s for this project (%s)", status, attentionPath(root))
	w.addNote(msg, false)
	return msg
}
