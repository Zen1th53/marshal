package tui

import (
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/tui/board/vm"
	"github.com/Zen1th53/marshal/internal/usage"
)

// The paused card: a stop that needs one choice from the owner
// (docs/design/board-v2/screen5.ans). It reads the run's own pause (the
// code, action and task the runtime recorded) or the Board's stall check
// (runStall), names the cause in plain words, lists the paused tasks and
// what of their work is kept, and says exactly what Enter does. Enter runs
// the same commands the inbox's stop entry and the owner's own /marshal
// commands run (boardv2_cards_keys.go).

// Pause causes, the card's Kind.
const (
	pauseQuota       = "quota"
	pauseAuth        = "auth"
	pauseOrphan      = "orphan"
	pauseTimeout     = "timeout"
	pauseLease       = "lease"
	pauseInterrupted = "interrupted"
	pauseStalled     = "stalled"
	pauseStopped     = "stopped"
	pauseNewPlan     = "new_plan"
	pauseOther       = "other"
)

// v2Pause is the paused card and what its keys act on.
type v2Pause struct {
	cause string
	card  vm.Paused
	// enter is what Enter runs, through actOnStop; wait is set instead
	// when Enter waits for the reset (no provider has room to move to).
	enter runStop
	wait  bool
	until time.Time
	// detail is the raw error d shows.
	detail string
	// target is the provider paused tasks move to, "" for none.
	target string
	tasks  []marshal.Task
}

// pauseCause names what stopped the run from its pause code and its raw
// detail, in the card's causes.
func pauseCause(pause *marshal.Pause) string {
	if pause == nil {
		return pauseOther
	}
	text := strings.ToLower(pause.Detail + " " + pause.Reason)
	switch {
	case pause.Kind == "quota" || pause.Code == marshal.CodeProviderQuota || pause.Code == marshal.CodeProviderLimit:
		return pauseQuota
	case pause.Code == marshal.CodeWorkerAuth || strings.Contains(text, "not signed in"):
		return pauseAuth
	case strings.Contains(text, "orphan task lane") || strings.Contains(text, "earlier attempt was interrupted"):
		return pauseOrphan
	case strings.Contains(text, "task time ran out") || strings.Contains(text, "lease expired") || strings.Contains(text, "lease lost") || strings.Contains(text, "lease was lost"):
		return pauseLease
	case strings.Contains(text, "timed out") || strings.Contains(text, "timeout") || strings.Contains(text, "deadline exceeded"):
		return pauseTimeout
	case pause.Code == marshal.CodeInterruptedOperation || pause.Code == marshal.CodeOperationRecovery || pause.Code == marshal.CodeInterruptedWorktree || pause.Code == marshal.CodeFailoverInterrupted:
		return pauseInterrupted
	}
	return pauseOther
}

// pauseTitles is each cause's title after "Paused · "; a quota title names
// the provider and its window instead.
var pauseTitles = map[string]string{
	pauseAuth:        "not signed in",
	pauseOrphan:      "interrupted lane",
	pauseTimeout:     "operation timed out",
	pauseLease:       "task lease lost",
	pauseInterrupted: "operation interrupted",
	pauseStalled:     "run stalled",
	pauseStopped:     "run stopped",
	pauseNewPlan:     "needs a new plan",
	pauseOther:       "needs your choice",
}

// v2PauseNow is the stop waiting for the owner's one choice now, or false.
// A pause that needs a new plan (use_plan) offers that choice: Enter opens
// the composer to tell the Marshal what to change, as the inbox's stop
// entry does (actOnStop), and the card says what the run asks for.
func (w *Workspace) v2PauseNow(now time.Time) (v2Pause, bool) {
	p := w.marshalPanel()
	if p == nil {
		return v2Pause{}, false
	}
	stop, ok := w.stopVisual(p).stopNotice()
	if !ok {
		return v2Pause{}, false
	}
	run := p.run
	var z v2Pause
	z.enter = stop
	switch {
	case p.Board.Run.State == marshal.AwaitingUser:
		z.cause = pauseCause(run.Pause)
		if run.Pause != nil {
			// Only a recorded technical cause is a raw error: the reason is
			// the card's own cause line, and d repeating it showed nothing.
			z.detail = strings.TrimSpace(run.Pause.Detail)
			z.until = run.Pause.Until
		}
		for _, t := range run.Tasks {
			if t.State == marshal.Escalated {
				z.tasks = append(z.tasks, t)
			}
		}
		if len(z.tasks) == 0 && run.Pause != nil && run.Pause.TaskID != "" {
			if t, ok := runTask(run, run.Pause.TaskID); ok {
				z.tasks = append(z.tasks, t)
			}
		}
	case p.Stopped:
		z.cause = pauseStopped
		z.detail = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(p.Note), "stopped: "))
	default:
		z.cause = pauseStalled
		for _, c := range stop.commands {
			if id, ok := strings.CutPrefix(c, "/marshal retry "); ok {
				id, _, _ = strings.Cut(id, " ")
				if t, ok := runTask(run, id); ok {
					z.tasks = append(z.tasks, t)
				}
			}
		}
		z.detail = stop.reason
	}
	if z.detail == stop.reason || z.detail == firstLine(stop.reason) {
		// The cause line is already on the card: no separate raw error.
		z.detail = ""
	}
	if stop.action == marshal.PauseUsePlan {
		z.cause = pauseNewPlan
	}
	c := vm.Paused{Kind: z.cause, Title: "Paused · " + pauseTitles[z.cause], Cause: stop.reason}
	qs, _ := w.usageQuotas()
	switch z.cause {
	case pauseQuota:
		w.quotaPause(&z, &c, qs, now)
	case pauseAuth:
		provider := pausedProvider(z.tasks)
		if provider != "" {
			c.Title = "Paused · " + provider + " not signed in"
			c.Cause = provider + " is not signed in for workers. To sign in, " + app.WorkerSigninInstruction(provider) + ", then press Enter."
		}
		z.enter = w.retryFresh(z, "retry "+taskList(z.tasks)+" in a fresh session, keeping its branch and commits, and resume the run")
	case pauseOrphan, pauseTimeout, pauseLease:
		if len(z.tasks) > 0 {
			z.enter = w.retryFresh(z, "start "+taskList(z.tasks)+" again in a fresh session, keeping its branch and commits, and resume the run")
		}
	case pauseNewPlan:
		c.Will = "open the composer to tell the Marshal what to change."
		if next := strings.TrimSpace(stop.next); next != "" && !strings.HasPrefix(next, "Press Enter") {
			// The run's own words, wrapped to the card's left column.
			c.Will += "\n" + strings.Join(wrapWords("The run asks: "+cleanBoard(next), 68), "\n")
		}
	}
	if c.Will == "" {
		c.Will = willText(z.enter)
	}
	for _, t := range z.tasks {
		c.Tasks = append(c.Tasks, w.pausedTask(p, t))
	}
	c.Actions = []vm.Key{{Key: "⏎", Label: enterLabel(z)}}
	if !z.until.IsZero() {
		c.Actions = append(c.Actions, vm.Key{Key: "w", Label: "wait until " + resetClock(z.until, now)})
	}
	if z.detail != "" {
		c.Actions = append(c.Actions, vm.Key{Key: "d", Label: "raw error"})
	}
	c.Actions = append(c.Actions, vm.Key{Key: "x", Label: "stop the run"})
	z.card = c
	return z, true
}

// quotaPause fills a limit pause: which provider and window ran out, when it
// resets, and where the paused tasks can go now. With a provider that has
// room, Enter moves them there (/marshal reassign, then /marshal resume): a
// new provider starts a fresh session on the kept branch. With none, Enter
// waits for the reset and resumes, as the quota card's r does.
func (w *Workspace) quotaPause(z *v2Pause, c *vm.Paused, qs []usage.Quota, now time.Time) {
	limited := map[string]bool{}
	for _, t := range z.tasks {
		if p := providerOf(t.Worker); p != "" {
			limited[p] = true
		}
	}
	provider := pausedProvider(z.tasks)
	window := ""
	for _, q := range qs {
		if q.Provider != provider {
			continue
		}
		if win, ok := q.Binding(); ok {
			window = windowName(win.Label)
			if z.until.IsZero() {
				z.until = win.ResetsAt
			}
		}
	}
	title := strings.TrimSpace(provider + " " + window + " limit")
	if provider == "" {
		title = "provider limit"
	}
	c.Title = "Paused · " + title
	if !z.until.IsZero() {
		c.Resets = resetClock(z.until, now)
	}
	who := w.pausedLanes(z.tasks)
	if who != "" {
		c.Cause = who + " hit the " + title + ". " + plural(len(z.tasks), "Its", "Their") + " work is kept."
	}
	for _, p := range providerLeftOrder {
		if limited[p] {
			continue
		}
		for _, q := range qs {
			if q.Provider != p {
				continue
			}
			if win, ok := q.Binding(); ok && usageLeft(win) > 0 && (z.target == "" || usageLeft(win) > w.leftOf(qs, z.target)) {
				z.target = p
			}
		}
	}
	if z.target != "" && len(z.tasks) > 0 {
		var commands []string
		for _, t := range z.tasks {
			commands = append(commands, "/marshal reassign "+t.PlanTaskID+" "+z.target)
		}
		commands = append(commands, "/marshal resume")
		z.enter = runStop{reason: z.enter.reason, commands: commands, verb: "move to " + z.target, action: marshal.PauseResume}
		c.Will = "move " + taskList(z.tasks) + " to " + z.target + " in fresh sessions, keep commits, and resume the run."
		return
	}
	z.wait = true
	z.enter = runStop{reason: z.enter.reason, verb: "wait and resume"}
	if z.until.IsZero() {
		c.Will = "resume the run as soon as a provider has limit left; the work stays saved."
	} else {
		c.Will = "resume the run at " + resetClock(z.until, now) + " or as soon as a provider has limit left; the work stays saved."
	}
}

// leftOf is provider's percent left in qs, 0 when unknown.
func (w *Workspace) leftOf(qs []usage.Quota, provider string) int {
	for _, q := range qs {
		if q.Provider == provider {
			if win, ok := q.Binding(); ok {
				return usageLeft(win)
			}
		}
	}
	return 0
}

// retryFresh is Enter for a stop whose tasks lost their session: each task
// starts again in a fresh session (/marshal retry <task> fresh-session) on
// its kept branch, then the run resumes.
func (w *Workspace) retryFresh(z v2Pause, will string) runStop {
	if len(z.tasks) == 0 {
		return z.enter
	}
	var commands []string
	for _, t := range z.tasks {
		commands = append(commands, "/marshal retry "+t.PlanTaskID+" fresh-session")
	}
	commands = append(commands, "/marshal resume")
	return runStop{reason: z.enter.reason, next: will, commands: commands, verb: "retry " + taskList(z.tasks), action: marshal.PauseRetryTask}
}

// willText is what Enter does, in words, from the commands it runs.
func willText(s runStop) string {
	if s.next != "" && !strings.HasPrefix(s.next, "Press Enter") {
		return strings.TrimSuffix(s.next, ".") + "."
	}
	var parts []string
	for _, c := range s.commands {
		f := strings.Fields(c)
		switch {
		case len(f) >= 3 && f[1] == "retry":
			what := "retry " + cleanBoard(f[2])
			if len(f) > 3 {
				what += " in a fresh session"
			}
			parts = append(parts, what)
		case len(f) >= 4 && f[1] == "reassign":
			parts = append(parts, "move "+cleanBoard(f[2])+" to "+f[3])
		case len(f) >= 2 && f[1] == "resume":
			parts = append(parts, "resume the run")
		case len(f) >= 2 && f[1] == "close":
			parts = append(parts, "close the run again")
		default:
			parts = append(parts, "run "+c)
		}
	}
	if len(parts) == 0 {
		return s.verb + "."
	}
	return strings.Join(parts, ", then ") + "."
}

// enterLabel is the Enter key's short label.
func enterLabel(z v2Pause) string {
	switch {
	case z.wait:
		return "wait and resume"
	case z.target != "":
		return "move to " + z.target
	}
	return z.enter.verb
}

// pausedTask is one paused task's row: its title, how far it got and what
// of its work is kept.
func (w *Workspace) pausedTask(p *MarshalPanel, t marshal.Task) vm.PausedTask {
	title := cleanBoard(t.PlanTaskID)
	if t.Title != "" {
		title += " " + firstLine(t.Title)
	}
	row := vm.PausedTask{Title: title, Progress: taskProgress(p, t)}
	tip := t.FailedAttemptCommit
	if tip == "" {
		tip = t.ResultCommit
	}
	if tip == "" {
		tip = t.Branch
	}
	switch {
	case t.Branch == "" && tip == "":
		row.Kept = "nothing yet"
	case t.BaseCommit != "" && tip != "":
		if n, ok := w.cachedCommits(t.BaseCommit, tip); ok {
			if n == 0 {
				row.Kept = "branch, no commits"
			} else {
				row.Kept = "branch + " + count(n, "commit", "commits")
			}
			break
		}
		row.Kept = "branch"
	default:
		row.Kept = "branch"
	}
	return row
}

// taskProgress is how far a paused task got: its lane's last action when
// the Board saw one, else what its commits say.
func taskProgress(p *MarshalPanel, t marshal.Task) string {
	for _, l := range p.Board.Lanes {
		if l.TaskID == t.PlanTaskID && strings.TrimSpace(l.Last) != "" {
			return firstLine(l.Last)
		}
	}
	switch {
	case t.ResultCommit != "":
		return "handed in"
	case t.FailedAttemptCommit != "":
		return "edits committed"
	case t.State == marshal.Returned:
		return "returned"
	}
	return "stopped mid-task"
}

// runTask is the run's task id.
func runTask(run marshal.Run, id string) (marshal.Task, bool) {
	for _, t := range run.Tasks {
		if t.PlanTaskID == id {
			return t, true
		}
	}
	return marshal.Task{}, false
}

// pausedProvider is the provider the paused tasks ran on, "" when they ran
// on several or none.
func pausedProvider(tasks []marshal.Task) string {
	provider := ""
	for _, t := range tasks {
		p := providerOf(t.Worker)
		if p == "" {
			p = t.Worker
		}
		if provider != "" && p != provider {
			return ""
		}
		provider = p
	}
	return provider
}

// pausedLanes names who stopped: the lanes the Board saw run the tasks
// ("agy-2 and agy-4"), else the tasks themselves.
func (w *Workspace) pausedLanes(tasks []marshal.Task) string {
	p := w.marshalPanel()
	var names []string
	for _, t := range tasks {
		name := ""
		if p != nil {
			for _, l := range p.Board.Lanes {
				if l.TaskID == t.PlanTaskID && l.Worker != "" {
					name = l.Worker
				}
			}
		}
		if name == "" {
			name = cleanBoard(t.PlanTaskID)
		}
		names = append(names, name)
	}
	return andList(names)
}

// taskList is "T06", "T06 and T09", "T01, T02 and T03".
func taskList(tasks []marshal.Task) string {
	var ids []string
	for _, t := range tasks {
		ids = append(ids, cleanBoard(t.PlanTaskID))
	}
	return andList(ids)
}

func andList(s []string) string {
	switch len(s) {
	case 0:
		return ""
	case 1:
		return s[0]
	}
	return strings.Join(s[:len(s)-1], ", ") + " and " + s[len(s)-1]
}
