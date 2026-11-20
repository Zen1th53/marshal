package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
	"github.com/Zen1th53/marshal/internal/usage"
)

// Low-limit warnings. A provider's window (or one agy quota group) under
// lowLimitPercent left is one Activity note, once per window per reset
// period; a window at 0% while tasks still wait for that provider is one
// more note naming who can take them. Before a plan runs, one note says
// how much limit its providers have left. Everything here works on the last
// cached reading (board_usage.go): nothing is read on the paint path.

// lowLimitPercent is the percent left under which a window is low.
const lowLimitPercent = 20

// limitWarned is what was said about one window in its current period.
type limitWarned struct {
	until time.Time // the window's reset when warned: a new period after it
	empty bool      // the "0% left, tasks wait" note was written
}

// windowName is a window label in plain words: "wk" is "weekly", so
// "Claude and GPT wk" reads "Claude and GPT weekly".
func windowName(label string) string {
	if label == "wk" {
		return "weekly"
	}
	if g, ok := strings.CutSuffix(label, " wk"); ok {
		return g + " weekly"
	}
	return label
}

// resetClock is when a window resets in the owner's words: the time of day
// within a day ("14:32"), the date further out ("21 Nov").
func resetClock(at, now time.Time) string {
	at = at.In(now.Location())
	if at.Sub(now) < 24*time.Hour {
		return at.Format("15:04")
	}
	return at.Format("2 Jan")
}

// lowWindowText is one low window as the note and the vitals row say it:
// "agy Claude and GPT 5h: 12% left · resets 14:32". A stale reading is
// marked "~" as every other limit figure is.
func lowWindowText(q usage.Quota, win usage.Window, now time.Time) string {
	prefix := ""
	if q.Stale {
		prefix = "~"
	}
	return fmt.Sprintf("%s %s: %s%d%% left · resets %s", q.Provider, windowName(win.Label), prefix, usageLeft(win), resetClock(win.ResetsAt, now))
}

// waitingTasks counts the run's tasks that wait to be dispatched to provider:
// queued, or back for rework.
func waitingTasks(b boardmodel.Board, provider string) int {
	n := 0
	for _, t := range b.Tasks {
		if (t.State == boardmodel.TaskQueued || t.State == boardmodel.TaskRework) && providerOf(t.Worker) == provider {
			n++
		}
	}
	return n
}

// otherProvider is the provider with the most limit left other than from, and
// that percent; "" when no other provider has any left.
func otherProvider(qs []usage.Quota, from string) (string, int) {
	best, bestLeft := "", 0
	for _, p := range providerLeftOrder {
		if p == from {
			continue
		}
		for _, q := range qs {
			if q.Provider != p {
				continue
			}
			if win, ok := q.Binding(); ok && usageLeft(win) > bestLeft {
				best, bestLeft = p, usageLeft(win)
			}
		}
	}
	return best, bestLeft
}

// lowLimitNotes is the notes a reading calls for, given what was already said
// (warned, updated in place) and the run on the Board (nil for none).
func lowLimitNotes(qs []usage.Quota, b *boardmodel.Board, warned map[string]limitWarned, now time.Time) []string {
	var notes []string
	for _, q := range qs {
		if !q.Known {
			continue
		}
		for _, win := range q.Windows {
			if !win.Known() || usageLeft(win) >= lowLimitPercent {
				continue
			}
			key := q.Provider + "|" + win.Label
			waiting := 0
			if b != nil && usageLeft(win) == 0 {
				waiting = waitingTasks(*b, q.Provider)
			}
			st, said := warned[key]
			if said && now.After(st.until) {
				said = false // the window reset since: a new period
			}
			if said && (st.empty || waiting == 0) {
				continue
			}
			text := lowWindowText(q, win, now)
			if waiting > 0 {
				text += ". " + waitingText(qs, q.Provider, waiting)
			}
			notes = append(notes, text)
			warned[key] = limitWarned{until: win.ResetsAt, empty: waiting > 0}
		}
	}
	return notes
}

// waitingText says that n tasks wait for a spent provider and who can take
// them: "3 tasks wait for agy; codex can take them (61% left)."
func waitingText(qs []usage.Quota, provider string, n int) string {
	head := fmt.Sprintf("%d tasks wait for %s", n, provider)
	them := "them"
	if n == 1 {
		head, them = "1 task waits for "+provider, "it"
	}
	if other, left := otherProvider(qs, provider); other != "" {
		return fmt.Sprintf("%s; %s can take %s (%d%% left).", head, other, them, left)
	}
	return head + "; no other provider has limit left."
}

// warnLowLimits writes the low-limit notes a new reading calls for.
func (w *Workspace) warnLowLimits(qs []usage.Quota, now time.Time) {
	var board *boardmodel.Board
	if p := w.marshalPanel(); p != nil {
		b := p.Board
		board = &b
	}
	w.usage.mu.Lock()
	if w.usage.warned == nil {
		w.usage.warned = map[string]limitWarned{}
	}
	notes := lowLimitNotes(qs, board, w.usage.warned, now)
	w.usage.mu.Unlock()
	for _, n := range notes {
		w.addNote(n, false)
	}
}

// planProviders is the providers the run's unfinished tasks are assigned to,
// in the order they first appear.
func planProviders(b boardmodel.Board) []string {
	var out []string
	for _, t := range b.Tasks {
		if t.State == boardmodel.TaskMerged || t.State == boardmodel.TaskCancelled {
			continue
		}
		if p := providerOf(t.Worker); p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// planLimitText is the one-line limit check for a plan of tasks tasks on
// providers: "Limits: codex 19% weekly (resets 21 Nov), 45% 5h — this plan
// has 18 tasks. Below 20%: codex weekly." low reports whether any window is
// under lowLimitPercent. Each provider shows its two lowest windows and every
// low one; a low window says when it resets.
func planLimitText(qs []usage.Quota, providers []string, tasks int, now time.Time) (text string, low bool) {
	var parts, lows []string
	for _, p := range providers {
		var q usage.Quota
		for _, x := range qs {
			if x.Provider == p {
				q = x
			}
		}
		var wins []usage.Window
		if q.Known {
			for _, win := range q.Windows {
				if win.Known() {
					wins = append(wins, win)
				}
			}
		}
		if len(wins) == 0 {
			parts = append(parts, p+" "+limitUnknown)
			continue
		}
		slices.SortStableFunc(wins, func(a, b usage.Window) int { return usageLeft(a) - usageLeft(b) })
		prefix := ""
		if q.Stale {
			prefix = "~"
		}
		var figs []string
		for i, win := range wins {
			left := usageLeft(win)
			if i >= 2 && left >= lowLimitPercent {
				continue
			}
			group, kind := "", win.Label
			if sp := strings.LastIndex(win.Label, " "); sp >= 0 {
				group, kind = win.Label[:sp+1], win.Label[sp+1:]
			}
			fig := fmt.Sprintf("%s%s%d%% %s", group, prefix, left, windowName(kind))
			if left < lowLimitPercent {
				fig += " (resets " + resetClock(win.ResetsAt, now) + ")"
				lows = append(lows, p+" "+windowName(win.Label))
			}
			figs = append(figs, fig)
		}
		parts = append(parts, p+" "+strings.Join(figs, ", "))
	}
	noun := "tasks"
	if tasks == 1 {
		noun = "task"
	}
	text = fmt.Sprintf("Limits: %s — this plan has %d %s", strings.Join(parts, " · "), tasks, noun)
	if len(lows) > 0 {
		text += ". Below 20%: " + strings.Join(lows, ", ") + "."
	}
	return text, len(lows) > 0
}

// planStarting reports whether the run's plan is about to run: drafted or
// just approved, with tasks and none finished yet.
func planStarting(b boardmodel.Board) bool {
	if b.Run.ID == "" || b.Run.Total == 0 || b.Run.Done > 0 {
		return false
	}
	switch b.Run.State {
	case marshal.Drafting, marshal.Approved, marshal.Dispatching:
		return true
	}
	return false
}

// planLimits is the limit check for the run's plan on the last reading; ok is
// false before the first reading or when the plan names no provider.
func (w *Workspace) planLimits(p *MarshalPanel) (text string, low, ok bool) {
	if p == nil {
		return "", false, false
	}
	providers := planProviders(p.Board)
	qs, read := w.usageQuotas()
	if !read || len(providers) == 0 {
		return "", false, false
	}
	text, low = planLimitText(qs, providers, p.Board.Run.Total, time.Now())
	return text, low, true
}

// notePlanLimits writes the plan's limit check to Activity once per plan,
// when the plan is about to run and the limits have been read.
func (w *Workspace) notePlanLimits(p *MarshalPanel) {
	if p == nil || !planStarting(p.Board) {
		return
	}
	key := p.Board.Run.ID + ":" + p.Board.PlanID
	w.usage.mu.Lock()
	done := w.usage.planNoted[key]
	w.usage.mu.Unlock()
	if done {
		return
	}
	text, _, ok := w.planLimits(p)
	if !ok {
		return
	}
	w.usage.mu.Lock()
	if w.usage.planNoted == nil {
		w.usage.planNoted = map[string]bool{}
	}
	done = w.usage.planNoted[key]
	w.usage.planNoted[key] = true
	w.usage.mu.Unlock()
	if !done {
		w.addNote(text, false)
	}
}
