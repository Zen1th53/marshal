package tui

import (
	"context"
	"sort"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
)

// taskModelWhy is the one sentence the registry's task.model row and w's own
// "no task" answer both show, so they cannot drift.
const taskModelWhy = "Highlight a task first (t, then j/k)."

// taskUnderKeyboard is the task w acts on: the one in the inspector, or the
// selected task while the Tasks pane has the focus. ok is false otherwise. It
// reads only in-memory state, so the registry's When may call it on the paint
// path.
func taskUnderKeyboard(w *Workspace) (string, bool) {
	ui := &w.boardUI
	if !w.boardVisible() || w.anyBodyView() {
		return "", false
	}
	p := w.marshalPanel()
	if p == nil {
		return "", false
	}
	_, stopped := (boardVisual{board: p.Board, note: p.Note, stopped: p.Stopped}).stopNotice()
	var id string
	switch {
	case ui.inspect != "":
		id = ui.inspect
	case ui.pane(len(w.boardInbox()) > 0 || stopped) == layout.Tasks:
		id = ui.task
	default:
		return "", false
	}
	if id == "" {
		return "", false
	}
	if _, ok := taskRow(p.Board, id); !ok {
		return "", false
	}
	return id, true
}

// boardTaskCandidates reads the four worker drivers' limits when the task-model
// picker opens, in driver order, over every installed one. Tests replace it to
// inject a fixed candidate list.
var boardTaskCandidates = (*Workspace).taskModelCandidates

// taskModelCandidates is marshalCandidates for a task: the same limit/busy
// reader over the four worker drivers (not the app.MarshalProviders() filter),
// so a task may move to any installed model the edition allows.
func (w *Workspace) taskModelCandidates(ctx context.Context) []pickerCand {
	return w.candidatesForDrivers(ctx, neutralProviders)
}

// openTaskModelPicker handles w on id: it answers plainly for a task whose
// model cannot change now, and otherwise opens the picker over the Board with
// the task's worker marked "now". The state table is P5-05's.
func (w *Workspace) openTaskModelPicker(ctx context.Context, id string) {
	p := w.marshalPanel()
	if p == nil {
		return
	}
	row, ok := taskRow(p.Board, id)
	if !ok {
		return
	}
	cid := cleanBoard(id)
	switch row.State {
	case boardmodel.TaskWorking:
		w.setOutput(cid+" is running on "+marshalDisplayName(row.Worker)+" now. Its model can change once it hands in, is sent back or gets stuck.", false)
		return
	case boardmodel.TaskReview, boardmodel.TaskAccepted, boardmodel.TaskMerged, boardmodel.TaskCancelled:
		w.setOutput(cid+" is finished; its model cannot change.", false)
		return
	case boardmodel.TaskQueued:
		if !app.OperatorCanMoveQueuedTask() {
			w.setOutput(cid+" has not started yet; in this edition its model can change only after it gets stuck.", false)
			return
		}
	case boardmodel.TaskParked, boardmodel.TaskRework:
		// The picker opens.
	default:
		return
	}
	cands := boardTaskCandidates(w, ctx)
	if len(cands) == 0 {
		w.setOutput("No coding agent is installed.", false)
		return
	}
	worker := row.Worker
	w.openPickerFor(pickerFor{
		title:   " WHO RUNS " + cid + " ",
		cands:   cands,
		current: worker,
		apply: func(ctx context.Context, choice string) {
			switch choice {
			case "":
				// Esc or m: keep the task where it is.
			case worker:
				w.setOutput(cid+" stays on "+marshalDisplayName(worker)+".", false)
			default:
				w.moveTask(ctx, id, choice)
			}
		},
	})
}

// moveTask applies a chosen model to id. A step in flight cannot take a
// revisioned save, so it queues the move in memory and drains it when the step
// ends (drainTaskMoves); otherwise it runs the move on the ordinary command
// lane and shows a plain answer.
func (w *Workspace) moveTask(ctx context.Context, id, worker string) {
	m := w.marshalSession()
	m.mu.Lock()
	busy := m.busy
	if busy {
		if m.nextTaskMoves == nil {
			m.nextTaskMoves = map[string]string{}
		}
		m.nextTaskMoves[id] = worker
	}
	m.mu.Unlock()
	if busy {
		w.setOutput("The Marshal is mid-step. "+cleanBoard(id)+" moves to "+marshalDisplayName(worker)+" when that step ends — unless it has started by then.", false)
		return
	}
	w.runCommandFunc(ctx, "model "+id, func(ctx context.Context) (string, error) {
		answer, err := boardMove(w, ctx, id, worker)
		if err != nil {
			return w.plainMoveError(id, worker, err), nil
		}
		return answer, nil
	})
}

// boardMove moves id to worker and, for a task that was parked or sent back,
// continues the run. It is a package var so a test can stand in for the
// Marshal calls it makes. It is assigned in init so its default does not form
// a package initialization cycle with marshalResume (through marshalSession).
var boardMove func(w *Workspace, ctx context.Context, id, worker string) (string, error)

func init() {
	boardMove = func(w *Workspace, ctx context.Context, id, worker string) (string, error) {
		parked := w.taskWaitsForRework(id)
		if _, err := w.marshalReassign(ctx, []string{id, worker}); err != nil {
			return "", err
		}
		return moveContinuation(w, id, worker, parked, func() error { _, err := w.marshalResume(ctx); return err }), nil
	}
}

// taskWaitsForRework reports whether id's Board state is parked or waiting for
// rework: the states whose move needs a resume to pick the task up. A queued
// task is left for the run that is already moving.
func (w *Workspace) taskWaitsForRework(id string) bool {
	p := w.marshalPanel()
	if p == nil {
		return false
	}
	row, ok := taskRow(p.Board, id)
	return ok && (row.State == boardmodel.TaskParked || row.State == boardmodel.TaskRework)
}

// moveContinuation is the operator's answer once the reassignment succeeded,
// and the resume it runs when the task was parked or sent back and the run no
// longer waits on the operator. It is the production half the seam keeps so a
// test exercises it.
func moveContinuation(w *Workspace, id, worker string, parked bool, resume func() error) string {
	answer := cleanBoard(id) + " will run on " + marshalDisplayName(worker) + "."
	if parked && w.marshalPanel().State != marshal.AwaitingUser {
		if err := resume(); err != nil {
			// The move happened; only the resume failed. Keep the move's
			// answer and record the detail.
			w.RecordActivity("resume after moving " + cleanBoard(id) + " failed: " + err.Error())
		}
	}
	return answer
}

// plainMoveError turns a reassignment service error into one plain sentence;
// the raw error is logged to Activity only for the ones with nothing to tell
// the operator.
func (w *Workspace) plainMoveError(id, worker string, err error) string {
	msg := err.Error()
	cid := cleanBoard(id)
	name := marshalDisplayName(worker)
	switch {
	case strings.Contains(msg, "cannot reassign to the same harness"):
		return cid + " already runs on " + name + "."
	case strings.Contains(msg, "only escalated tasks can be reassigned"):
		return cid + " cannot move right now; it can once it gets stuck."
	case strings.Contains(msg, "is governed by its approved plan"):
		return cid + "'s model is set by the approved plan; change the plan to change it."
	case strings.Contains(msg, "does not support task mode"):
		return name + " cannot run this kind of task."
	case strings.Contains(msg, "cannot reassign task with escalation reason:"):
		_, reason, _ := strings.Cut(msg, "cannot reassign task with escalation reason:")
		return cid + " cannot move: " + cleanBoard(strings.TrimSpace(reason)) + "."
	default:
		w.RecordActivity("move " + cid + " to " + worker + " failed: " + msg)
		return cid + " could not move right now."
	}
}

// drainTaskMoves applies the model moves queued while a step ran. It runs after
// the step finishes (onIdle), before the Marshal switch queue. A task that has
// started since it was chosen is dropped with a plain note; the rest move now
// that nothing is running. The command lane runs one command at a time, so it
// starts one move (in task-id order) and leaves the rest queued; the end of that
// command (runCommandFunc) drains the next. While a step or a command is still
// running nothing is taken off the queue.
func (w *Workspace) drainTaskMoves(ctx context.Context) {
	m := w.marshalSession()
	for {
		m.mu.Lock()
		if m.busy || w.commandBusy.Load() || len(m.nextTaskMoves) == 0 {
			m.mu.Unlock()
			return
		}
		ids := make([]string, 0, len(m.nextTaskMoves))
		for id := range m.nextTaskMoves {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		id := ids[0]
		worker := m.nextTaskMoves[id]
		delete(m.nextTaskMoves, id)
		m.mu.Unlock()

		row, ok := boardmodel.TaskRow{}, false
		if p := w.marshalPanel(); p != nil {
			row, ok = taskRow(p.Board, id)
		}
		if !ok || !taskModelMovable(row.State) {
			w.setOutput(cleanBoard(id)+" already started on "+marshalDisplayName(row.Worker)+"; its model was not changed.", false)
			continue
		}
		w.moveTask(ctx, id, worker)
		return
	}
}

// taskModelMovable reports whether a task in state s can still take a model
// move: it has not started, is parked, or is waiting for rework.
func taskModelMovable(s boardmodel.TaskState) bool {
	switch s {
	case boardmodel.TaskQueued, boardmodel.TaskParked, boardmodel.TaskRework:
		return true
	}
	return false
}
