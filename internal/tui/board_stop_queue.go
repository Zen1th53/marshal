package tui

import (
	"context"
	"slices"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// The stop's action while a step still runs. A run can wait for the owner
// (a task escalated, the paused card offers "Enter retries it") while the
// Marshal is still collecting another task. Every command the stop runs
// (/marshal retry, reassign, resume, close) is refused while a step runs, so
// Enter used to fail each time with "a Marshal operation is already running;
// /marshal stop first" — one refused retry and one error-log line per press,
// and a message that told the owner to stop the step in flight. Instead the
// first press queues the action once and says so; later presses change
// nothing; the step is never stopped; and the action runs when the step ends,
// if the Board still asks for it.

// queueStopWhileBusy queues commands, the stop's action, while a Marshal
// step runs, and reports whether it did; the caller runs them now otherwise.
func (w *Workspace) queueStopWhileBusy(stop runStop, commands []string) bool {
	m := w.marshalSession()
	m.mu.Lock()
	if !m.busy {
		m.mu.Unlock()
		return false
	}
	holder := m.currentBlockerLocked()
	m.nextStop = append([]string(nil), commands...)
	m.mu.Unlock()
	receipt := marshal.RefusalReceipt{
		Code:         marshal.RefusalCodeBusy,
		Blocker:      holder,
		Effects:      marshal.EffectsNone,
		NextCommands: []string{"/marshal status", "/marshal stop"},
		Message:      errMarshalBusy.Error(),
	}
	w.setOutputWithRefusal(stopQueuedAnswer(stop, commands, holder), false, receipt)
	return true
}

// stopQueuedAnswer is the one line a press shows while a step runs. It is the
// same line however often Enter is pressed.
func stopQueuedAnswer(stop runStop, commands []string, holder *marshal.OperationHolder) string {
	stop.commands = commands
	lead := "a step"
	if holder != nil && holder.ID != "" && holder.ID != "active" && holder.Title != "" && holder.Title != "Marshal operation" {
		lead = holder.Label()
	}
	return "The Marshal is finishing " + lead + ". When it ends, it will " + willText(stop) + " Nothing was stopped."
}

// queuedStopDrain is drainQueuedStop for marshalSession's onIdle, set in
// init: a direct call there would make an initialization cycle through
// boardStopHandle.
var queuedStopDrain func(*Workspace, context.Context)

func init() { queuedStopDrain = (*Workspace).drainQueuedStop }

// drainQueuedStop runs the stop's action queued while a step ran, once the
// step and the command lane are free. It runs only when the Board still shows
// a stop asking for the same commands; when the run moved on it is dropped
// with a plain note.
func (w *Workspace) drainQueuedStop(ctx context.Context) {
	m := w.marshalSession()
	m.mu.Lock()
	if m.busy || w.commandBusy.Load() || len(m.nextStop) == 0 {
		m.mu.Unlock()
		return
	}
	queued := m.nextStop
	m.nextStop = nil
	m.mu.Unlock()
	if stop, ok := w.currentStopFor(queued); ok {
		w.actOnStop(ctx, stop)
		return
	}
	w.setOutput("The run moved on while the Marshal finished its step, so the action you chose did not run. The Board shows what it needs now.", false)
}

// currentStopFor is the stop the Board shows now whose Enter runs commands:
// the paused card's choice or the inbox's stop entry.
func (w *Workspace) currentStopFor(commands []string) (runStop, bool) {
	p := w.marshalPanel()
	if p == nil {
		return runStop{}, false
	}
	same := func(s runStop) bool {
		c := s.commands
		if len(c) == 0 {
			c = []string{"/marshal resume"}
		}
		return slices.Equal(c, commands)
	}
	if z, ok := w.v2PauseNow(boardNow()); ok && !z.wait && same(z.enter) {
		return z.enter, true
	}
	if s, ok := w.stopVisual(p).stopNotice(); ok && same(s) {
		return s, true
	}
	return runStop{}, false
}
