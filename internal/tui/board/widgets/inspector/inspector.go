// Package inspector renders one task's run-scoped Board projection.
package inspector

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Zen1th53/marshal/internal/tui/board/model"
)

// View owns only navigation state. The Board owns all displayed data: the
// caller keeps one View while the inspector stays open and hands it each
// new projection with SetBoard, so the navigation survives repaints.
type View struct {
	board     model.Board
	taskID    string
	depth     int
	down      []string
	allEvents bool
}

func New(board model.Board, taskID string) *View {
	return &View{board: board, taskID: taskID}
}

// SetBoard replaces the projection the view reads and keeps its navigation.
func (v *View) SetBoard(board model.Board) { v.board = board }

// TaskID is the task the view shows now.
func (v *View) TaskID() string { return v.taskID }

// Depth is how many levels the view walked down (positive) or up
// (negative) the delegation chain from the task it opened on.
func (v *View) Depth() int { return v.depth }

// AllEvents reports whether the timeline shows every event.
func (v *View) AllEvents() bool { return v.allEvents }

// Key handles local inspector navigation: e toggles all events, Enter walks
// down the delegation chain to a child task, Backspace up to the parent.
// It returns false when the key has nothing to do; the caller closes the
// inspector on Esc, and on Backspace at the top of the chain.
func (v *View) Key(key rune) bool {
	switch key {
	case 'e':
		v.allEvents = !v.allEvents
		return true
	case '\n', '\r':
		if child := v.child(); child != "" {
			if n := len(v.down); n > 0 && v.down[n-1] == child {
				v.down = v.down[:n-1]
			} else {
				v.down = nil
			}
			v.taskID = child
			v.depth++
			return true
		}
	case '\b', 127:
		if parent := v.parent(); parent != "" {
			v.down = append(v.down, v.taskID)
			v.taskID = parent
			v.depth--
			return true
		}
	}
	return false
}

// parent is the shown task's parent when the Board has it.
func (v *View) parent() string {
	if task, ok := v.task(); ok && task.ParentID != "" && v.hasTask(task.ParentID) {
		return task.ParentID
	}
	return ""
}

// child is the task Enter walks down to: the one Backspace came up from,
// else the first task delegated from the shown one.
func (v *View) child() string {
	if n := len(v.down); n > 0 && v.hasTask(v.down[n-1]) {
		return v.down[n-1]
	}
	for _, t := range v.board.Tasks {
		if t.ParentID == v.taskID && t.ID != v.taskID {
			return t.ID
		}
	}
	return ""
}

func (v *View) hasTask(id string) bool {
	for _, t := range v.board.Tasks {
		if t.ID == id {
			return true
		}
	}
	return false
}

func (v *View) task() (model.TaskRow, bool) {
	for _, t := range v.board.Tasks {
		if t.ID == v.taskID {
			return t, true
		}
	}
	return model.TaskRow{}, false
}

func (v *View) Breadcrumbs() []string {
	crumbs := []string{v.board.Run.ID}
	if v.board.PlanID != "" {
		crumbs = append(crumbs, v.board.PlanID)
	}
	t, ok := v.task()
	if !ok {
		return crumbs
	}
	parents := []string{}
	seen := map[string]bool{t.ID: true}
	for parent := t.ParentID; parent != "" && !seen[parent]; {
		seen[parent] = true
		parents = append(parents, parent)
		parent = ""
		for _, candidate := range v.board.Tasks {
			if candidate.ID == parents[len(parents)-1] {
				parent = candidate.ParentID
				break
			}
		}
	}
	slices.Reverse(parents)
	crumbs = append(crumbs, parents...)
	crumbs = append(crumbs, t.ID)
	if t.Worker != "" {
		crumbs = append(crumbs, t.Worker)
	}
	return crumbs
}

// Render returns logical lines. The Board frame owns cell clipping, padding,
// overlay placement, and the docked pane's width.
func (v *View) Render(width, height int) []string {
	if width <= 0 || height <= 0 {
		return nil
	}
	t, ok := v.task()
	if !ok {
		return []string{strings.Join(v.Breadcrumbs(), " › "), "Task unavailable"}
	}
	crumb := strings.Join(v.Breadcrumbs(), " › ")
	if v.board.PlanID != "" {
		crumb = strings.Replace(crumb, " › "+v.board.PlanID+" › ", " › plan "+v.board.PlanID+" › ", 1)
	}
	lines := []string{crumb, fmt.Sprintf("%s  %s  %s", t.ID, t.Title, t.State), "", "IDENTITY"}
	provider, _, _ := strings.Cut(t.Worker, "-")
	branch := t.Branch
	if branch == "" && v.board.Run.ID != "" {
		branch = "marshal/" + strings.ToLower(v.board.Run.ID) + "/" + strings.ToLower(t.ID)
	}
	lines = append(lines, fmt.Sprintf("  worker %s · provider %s · branch %s", t.Worker, provider, branch), "", "AUTHORITY", "  scope: "+strings.Join(t.Scope, ", "))
	hosts, tools := []string{}, []string{}
	for _, item := range v.board.Inbox {
		if item.Request.TaskID != t.ID {
			continue
		}
		if item.Kind == model.InboxEgress {
			hosts = append(hosts, item.Request.Object)
		} else {
			tools = append(tools, item.Request.Object)
		}
	}
	if len(t.AuthorizedHosts) > 0 {
		hosts = t.AuthorizedHosts
	}
	lines = append(lines, "  hosts: "+displayList(hosts))
	for _, check := range t.Checks {
		tools = append(tools, check)
	}
	if len(t.AuthorizedTools) > 0 {
		tools = t.AuthorizedTools
	}
	lines = append(lines, "  tools: "+displayList(tools))
	lines = append(lines, fmt.Sprintf("  budget: %d task tokens · rework %d/%d", v.board.Budget.Tokens.Task, t.Returns, t.ReworkLimit), "", "EVIDENCE")
	if t.EvidenceSummary != "" {
		lines = append(lines, "  "+t.EvidenceSummary)
	} else {
		if len(t.Criteria) == 0 {
			lines = append(lines, "  no claims")
		}
		claimState := "pending"
		switch t.State {
		case model.TaskReview, model.TaskParked, model.TaskRework:
			claimState = "unverified"
		case model.TaskAccepted:
			claimState = "accepted"
		case model.TaskMerged:
			claimState = "verified"
		}
		for _, claim := range t.Criteria {
			lines = append(lines, "  "+claimState+": "+claim)
		}
	}
	lines = append(lines, codeEvidenceLines(v.board, t.ID)...)
	lines = append(lines, "", "TIMELINE")
	timeline := v.timeline(t.ID)
	rows, _ := ActionRows(v.Actions(), width)
	tail := 5 + len(rows) // "", ACTIONS, its line, the rows, "", the hint
	room := max(0, height-len(lines)-tail)
	lines = append(lines, timeline[:min(len(timeline), room)]...)
	lines = append(lines, "", "ACTIONS")
	if item, ok := v.decision(); ok {
		lines = append(lines, "  waiting: "+string(item.Kind)+" "+item.Request.Object+" · a key opens its confirmation")
	} else {
		lines = append(lines, "  no pending decision")
	}
	lines = append(lines, rows...)
	lines = append(lines, "", v.hint())
	if len(lines) > height {
		lines = lines[:height]
	}
	return lines
}

// Action is one button of the inspector's action row: the key that does
// it and what it does. A click on the button is that key.
type Action struct {
	Key   rune
	Label string
}

// decision is the request waiting in the inbox for the shown task.
func (v *View) decision() (model.InboxItem, bool) {
	for _, item := range v.board.Inbox {
		if item.Request.TaskID == v.taskID {
			return item, true
		}
	}
	return model.InboxItem{}, false
}

// Actions is the action row for the shown task (mockup 03), only what
// applies: the decision keys while a decision waits for the task in the
// inbox (a hard gate has no allow-for-run, as the inbox offers none), and
// o while its worker has a pane to show. Each decision key opens the
// inbox's confirmation for it; none decides.
func (v *View) Actions() []Action {
	var out []Action
	if item, ok := v.decision(); ok {
		if item.Gate == model.GateTyped {
			out = append(out, Action{'a', "approve (type id)"})
		} else {
			out = append(out, Action{'A', "approve-all"}, Action{'a', "approve"})
		}
		out = append(out, Action{'d', "deny"}, Action{'r', "rework"}, Action{'x', "cancel"})
	}
	for _, l := range v.board.Lanes {
		if l.TaskID == v.taskID && l.Pane != "" {
			out = append(out, Action{'o', "open pane"})
			break
		}
	}
	return out
}

// Button is one action placed on the action rows: its row and its
// cells [X0, X1) there.
type Button struct {
	Action
	Row, X0, X1 int
}

// ActionRows lays the actions out as buttons, "  [A] approve-all  [a]
// approve ...", wrapping onto another row where width runs out. It is
// empty when nothing applies.
func ActionRows(actions []Action, width int) ([]string, []Button) {
	var rows []string
	var buttons []Button
	line, x := "", 0
	for _, a := range actions {
		text := "[" + string(a.Key) + "] " + a.Label
		n := len([]rune(text))
		sep := "  "
		if line == "" {
			sep = "  "
		}
		if line != "" && x+len(sep)+n > width {
			rows = append(rows, line)
			line, x, sep = "", 0, "  "
		}
		line += sep + text
		x += len(sep)
		buttons = append(buttons, Button{Action: a, Row: len(rows), X0: x, X1: x + n})
		x += n
	}
	if line != "" {
		rows = append(rows, line)
	}
	return rows, buttons
}

// hint lists only the keys that do something on the shown task.
func (v *View) hint() string {
	keys := []string{"e all events"}
	if v.allEvents {
		keys[0] = "e state changes"
	}
	if child := v.child(); child != "" {
		keys = append(keys, "Enter "+child)
	}
	if parent := v.parent(); parent != "" {
		keys = append(keys, "Backspace "+parent)
	}
	return strings.Join(append(keys, "Esc close"), " · ")
}

func displayList(items []string) string {
	if len(items) == 0 {
		return "none"
	}
	return strings.Join(items, ", ")
}

func (v *View) timeline(taskID string) []string {
	var lines []string
	previous, priorState := "run start", model.TaskQueued
	for _, e := range v.board.Events {
		if e.TaskID != taskID && e.TaskID != "" {
			continue
		}
		state, changed := model.NewActivityLine(e)
		if !v.allEvents && !changed {
			continue
		}
		cause := previous
		if e.TaskID == "" {
			cause = "run"
		}
		result := ""
		if changed {
			result = state.Detail
		}
		if result == "" {
			result, _ = e.Data["reason"].(string)
		}
		result, _, _ = strings.Cut(result, "\n")
		transition := string(e.Type)
		if changed && state.State != "" {
			transition = string(priorState) + " → " + string(state.State)
		}
		line := fmt.Sprintf("  %s ← %s [%s] %s", transition, cause, e.ID, result)
		lines = append(lines, line)
		if changed && e.TaskID == taskID {
			previous = e.ID
			priorState = state.State
		}
	}
	if len(lines) == 0 {
		if task, ok := v.task(); ok {
			return []string{"  queued → " + string(task.State)}
		}
		return []string{"  no events"}
	}
	slices.Reverse(lines)
	return lines
}
