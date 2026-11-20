package tui

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"
)

// The keys of the Board v2 cards. The decision card takes keys first, as it
// sits over the Board; the paused card takes them when no decision is shown.
// Every key runs an operation the Board already has: opening the review in
// the browser (openArchitectureReview), the plan gate's armed reply
// (sendGate), the inbox's own decision keys (handleBoardInboxKey), the stop
// commands the inbox's stop entry runs (actOnStop) and the quota card's
// auto-resume (armQuotaResume). Nothing here accepts an architecture.

// handleV2CardKey is a key while a Board v2 card is shown; it reports
// whether a card took it.
func (w *Workspace) handleV2CardKey(event KeyEvent) bool {
	now := time.Now()
	if d, ok := w.v2DecisionNow(now); ok {
		return w.handleV2DecisionKey(event, d)
	}
	if z, ok := w.v2PauseNow(now); ok {
		return w.handleV2PauseKey(event, z)
	}
	return false
}

// handleV2DecisionKey is the decision card's keys.
func (w *Workspace) handleV2DecisionKey(event KeyEvent, d v2Decision) bool {
	ctx := w.workers.context()
	s := w.v2CardState()
	if d.kind == decisionPlan {
		if armed := w.v2PlanArmed(); armed != "" {
			s.mu.Lock()
			s.planArmed = ""
			s.mu.Unlock()
			if armed == d.key && event.Type == KeyEnter {
				w.sendGate(ctx, d.gate)
			} else {
				w.memoryAnswer("Reply not sent.", false)
			}
			return true
		}
	}
	if d.kind == decisionInbox && w.inboxView.Confirming() {
		return w.handleBoardInboxKey(ctx, event)
	}
	if event.Type == KeyEsc {
		// "later": the card steps aside until another decision waits; the
		// inbox still lists this one.
		s.mu.Lock()
		s.dismissed = d.key
		s.mu.Unlock()
		w.requestRepaint()
		return true
	}
	switch d.kind {
	case decisionArchitecture:
		switch {
		case event.Type == KeyEnter:
			if !boardAcceptsArchitecture {
				w.memoryAnswer("Only you can accept the architecture, in the browser: a opens it, c copies its link.", false)
			}
			return true
		case isRune(event, 'a'):
			w.openArchitectureReview()
			return true
		case isRune(event, 'c'):
			w.copyCardLink(d.link)
			return true
		case isRune(event, 'k'):
			w.openSay()
			w.memoryAnswer("Say what to change in the architecture; the Marshal reads it in the chat.", false)
			return true
		}
	case decisionPlan:
		switch {
		case event.Type == KeyEnter:
			if d.gate.sent {
				w.armGate(d.gate) // says it was sent already
				return true
			}
			s.mu.Lock()
			s.planArmed = d.key
			s.mu.Unlock()
			w.memoryAnswer("Press Enter again to send “"+d.gate.reply+"” to the Marshal as your reply. Any other key cancels.", false)
			return true
		case isRune(event, '7'):
			if !w.switchBodyView(event) {
				w.setOutput("That view has nothing to read in this session.", false)
			}
			return true
		case isRune(event, 'k'):
			w.openSay()
			return true
		}
	case decisionInbox:
		if event.Type == KeyEnter || isRune(event, 'a') || isRune(event, 'd') {
			return w.handleBoardInboxKey(ctx, event)
		}
	}
	return false
}

// handleV2PauseKey is the paused card's keys: Enter does what the card says,
// w waits for the reset, d shows the raw error, x stops the run.
func (w *Workspace) handleV2PauseKey(event KeyEvent, z v2Pause) bool {
	ctx := w.workers.context()
	switch {
	case event.Type == KeyEnter:
		w.actOnPause(ctx, z)
		return true
	case isRune(event, 'w'):
		if z.until.IsZero() {
			w.memoryAnswer("This stop has no reset time to wait for; Enter does what the card says.", false)
			return true
		}
		w.armQuotaResume(context.Background(), z.until)
		w.memoryAnswer("Waiting: the run resumes at "+resetClock(z.until, time.Now())+" or as soon as a provider has limit left.", false)
		return true
	case isRune(event, 'd'):
		if z.detail == "" {
			w.memoryAnswer("No raw error was recorded for this stop.", false)
		} else {
			w.memoryAnswer("Raw error: "+z.detail, true)
		}
		return true
	case isRune(event, 'x'):
		w.stopPausedRun(ctx)
		return true
	}
	return false
}

// actOnPause is Enter on the paused card, one handler for every cause:
// v2PauseNow chose what the cause needs (move the tasks to a provider with
// room, retry them in fresh sessions, the run's own resume), and this runs
// it, or waits for the reset when no provider has room.
func (w *Workspace) actOnPause(ctx context.Context, z v2Pause) {
	if z.wait {
		w.armQuotaResume(context.Background(), z.until)
		w.memoryAnswer("No provider has limit left yet: the run resumes as soon as one has, the work stays saved.", false)
		return
	}
	w.actOnStop(ctx, z.enter)
}

// stopPausedRun is x on the paused card: any armed auto-resume is cancelled
// and any Marshal operation stops (/marshal stop); the run and its work are
// kept, and /marshal resume continues it.
func (w *Workspace) stopPausedRun(ctx context.Context) {
	w.disarmQuotaResume()
	resp, err := boardStopHandle(w, ctx, "/marshal stop")
	if err != nil {
		w.memoryAnswer("Could not stop the run: "+err.Error(), true)
		return
	}
	_ = resp
	w.RecordActivity("Stopped here. The run and its work are kept; /marshal resume continues it.")
	w.memoryAnswer("Stopped. The run and its work are kept; /marshal resume continues it.", false)
}

// v2ClipboardWrite is the seam the copy key writes through; nil writes an
// OSC 52 sequence to the terminal, which a local terminal (and tmux with
// set-clipboard on) puts on the clipboard, even over SSH.
var v2ClipboardWrite func(w *Workspace, text string) bool

// copyCardLink puts the review link on the clipboard.
func (w *Workspace) copyCardLink(link string) {
	if link == "" {
		w.memoryAnswer("No review link yet: a opens the architecture and writes it.", false)
		return
	}
	write := v2ClipboardWrite
	if write == nil {
		write = func(w *Workspace, text string) bool {
			if w.terminal == nil || w.terminal.out == nil || !w.terminal.IsTerminal() {
				return false
			}
			_, err := fmt.Fprintf(w.terminal.out, "\x1b]52;c;%s\x07", base64.StdEncoding.EncodeToString([]byte(text)))
			return err == nil
		}
	}
	if !write(w, link) {
		w.memoryAnswer("Could not reach the clipboard here; the link is in "+architectureURLFile+".", true)
		return
	}
	w.memoryAnswer("Copied the review link.", false)
}

func isRune(event KeyEvent, r rune) bool { return event.Type == KeyRune && event.Rune == r }
