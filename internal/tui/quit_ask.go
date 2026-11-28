package tui

import (
	"context"
	"strconv"
	"strings"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/tmux"
)

// Quitting ends the tmux session MARSHAL made (endOwnedSession): every
// worker, the Marshal chat and every other terminal attached to it. While
// work runs, or while another terminal shows this Board, q therefore asks
// first and offers detaching this terminal instead, which leaves everything
// running. With nothing running and no other terminal, q quits at once.

// quitAskKind is what the open quit question offers.
type quitAskKind int32

const (
	quitAskNone quitAskKind = iota
	// quitAskDetach offers detach (the default) or stop everything.
	quitAskDetach
	// quitAskStop is outside tmux, where nothing can keep running once
	// this process ends: stop everything, or stay.
	quitAskStop
)

// quitDetachedNote is what the Board says after this terminal detached; it
// is the first thing seen on return.
const quitDetachedNote = "Detached. Marshal and the work kept running; run `marshal` in this project to return."

// quitHold is why q asks before quitting: work that is running and the
// other terminals attached to this session.
type quitHold struct {
	working bool
	others  int
}

func (h quitHold) any() bool { return h.working || h.others > 0 }

// quitAskStatus is the footer line of the open quit question.
func quitAskStatus(kind quitAskKind, h quitHold) string {
	what := "Work is still running."
	if !h.working {
		what = "Another terminal shows this Marshal."
	}
	if kind == quitAskStop {
		return what + " Quitting stops it. s: stop everything · Esc: stay"
	}
	stop := "s: stop everything"
	if h.others > 0 {
		stop += " (closes the other terminal too)"
	}
	return what + " ⏎ or d: detach (work keeps running) · " + stop + " · Esc: stay"
}

// requestQuit is q: it quits at once when nothing would be lost, and opens
// the quit question otherwise.
func (w *Workspace) requestQuit(ctx context.Context) {
	h := quitHold{working: w.workRunning(), others: w.otherClients(ctx)}
	if !h.any() {
		w.requestExit()
		return
	}
	kind := quitAskDetach
	if !w.isTmuxActive() {
		kind = quitAskStop
	}
	w.mu.Lock()
	w.quitAsk, w.quitAskHold = kind, h
	w.mu.Unlock()
}

// quitAsking is the open quit question, quitAskNone for none.
func (w *Workspace) quitAsking() (quitAskKind, quitHold) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.quitAsk, w.quitAskHold
}

func (w *Workspace) closeQuitAsk() {
	w.mu.Lock()
	w.quitAsk, w.quitAskHold = quitAskNone, quitHold{}
	w.mu.Unlock()
}

// handleQuitAskKey answers the open quit question with event: Enter or d
// detaches (where tmux can), s stops everything, any other key stays. It
// reports whether the question took the key and whether the workspace
// exits.
func (w *Workspace) handleQuitAskKey(ctx context.Context, event KeyEvent) (took, exit bool) {
	kind, _ := w.quitAsking()
	if kind == quitAskNone {
		return false, false
	}
	if event.Type == KeyMouse || event.Type == KeyUnknown {
		// A pointer move or a report the terminal sent on its own is not
		// an answer; the question stays.
		return true, false
	}
	w.closeQuitAsk()
	switch {
	case isRune(event, 's'):
		w.requestExit()
		return true, true
	case kind == quitAskDetach && (event.Type == KeyEnter || isRune(event, 'd')):
		if err := w.detachThisClient(ctx); err != nil {
			w.setOutput("Could not detach: "+err.Error()+". Nothing was stopped; tmux's own detach key (Ctrl+B then d) also works.", true)
			return true, false
		}
		w.setOutput(quitDetachedNote, false)
		return true, false
	}
	w.setOutput("Still here. Nothing was stopped.", false)
	return true, false
}

// workRunning reports whether quitting would stop work in progress: a
// command or Marshal step running here, a worker open, or a run that is
// executing. It reads only in-memory state.
func (w *Workspace) workRunning() bool {
	if w.commandBusy.Load() || w.marshalStepRunning() {
		return true
	}
	w.tmuxMu.Lock()
	for _, a := range w.tmuxActiveWins {
		if a != nil && a.role != "marshal-chat" {
			w.tmuxMu.Unlock()
			return true
		}
	}
	w.tmuxMu.Unlock()
	if p := w.marshalPanel(); p != nil && !p.Stopped {
		switch p.State {
		case "", marshal.Drafting, marshal.AwaitingUser, marshal.Closed:
		default:
			return true
		}
	}
	return false
}

// tmuxClients is this session's attached terminals, most recently used
// first.
func (w *Workspace) tmuxClients(ctx context.Context) ([]string, error) {
	w.tmuxMu.Lock()
	session := w.tmuxSession
	w.tmuxMu.Unlock()
	if session == "" {
		return nil, nil
	}
	out, err := tmux.RunCommand(ctx, "list-clients", "-t", "="+session, "-F", "#{client_activity}\t#{client_name}")
	if err != nil {
		return nil, err
	}
	type client struct {
		at   int64
		name string
	}
	var cs []client
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		at, name, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok || name == "" {
			continue
		}
		n, _ := strconv.ParseInt(at, 10, 64)
		cs = append(cs, client{n, name})
	}
	// Most recent first; a stable insertion sort keeps tmux's order on ties.
	for i := 1; i < len(cs); i++ {
		for j := i; j > 0 && cs[j].at > cs[j-1].at; j-- {
			cs[j], cs[j-1] = cs[j-1], cs[j]
		}
	}
	names := make([]string, len(cs))
	for i, c := range cs {
		names[i] = c.name
	}
	return names, nil
}

// otherClients is how many terminals besides this one show this session;
// 0 outside tmux or when tmux cannot say.
func (w *Workspace) otherClients(ctx context.Context) int {
	if !w.isTmuxActive() {
		return 0
	}
	cs, err := w.tmuxClients(ctx)
	if err != nil || len(cs) < 2 {
		return 0
	}
	return len(cs) - 1
}

// detachThisClient detaches the terminal that pressed the key, the most
// recently used client of this session, and leaves every other terminal,
// the Marshal and the workers running.
func (w *Workspace) detachThisClient(ctx context.Context) error {
	cs, err := w.tmuxClients(ctx)
	if err != nil {
		return err
	}
	if len(cs) == 0 {
		return errNoClient
	}
	_, err = tmux.RunCommand(ctx, "detach-client", "-t", cs[0])
	return err
}

type quitError string

func (e quitError) Error() string { return string(e) }

const errNoClient = quitError("no terminal is attached to this session")
