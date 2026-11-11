package tui

import (
	"regexp"
	"strings"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

// runStop is why the run on the Board no longer moves on its own and what
// the operator does next: it waits for the operator (awaiting_user, the
// run's pause reason and resolutions) or its execution stopped on an error
// (the panel's "stopped: …" note). The Board shows it in the header's
// state chip, as the footer's first line and as the inbox's first entry.
type runStop struct {
	// waiting is true for awaiting_user, false for a stopped execution.
	waiting bool
	// reason is one line: why the run stopped.
	reason string
	// next is what resolves it, in the run's words; commands are the
	// exact commands it names, in order, "/marshal resume" when it names
	// none.
	next     string
	commands []string
	code     string
	verb     string
	action   marshal.PauseAction
}

// stopNotice is the run's stop, if it stopped.
func (v boardVisual) stopNotice() (runStop, bool) {
	run := v.board.Run
	if run.State == marshal.AwaitingUser {
		s := runStop{waiting: true, reason: firstLine(run.Paused), next: firstLine(strings.Join(run.Next, "; ")), code: run.PauseCode, action: run.PauseAction}
		if s.reason == "" {
			s.reason = "The run needs your decision."
		}
		task := run.PauseTask
		if s.action == "" {
			// Paused by an older build: its resolution named the commands.
			s.action, task = legacyPauseAction(strings.Join(run.Next, "; "))
		}
		s.verb = stopVerb(s.action)
		s.commands = pauseCommands(s.action, task)
		if s.next == "" {
			s.next = "Press Enter to " + s.verb + "."
		}
		return s, true
	}
	if v.stopped && run.State != marshal.Closed {
		s := runStop{reason: "The run stopped unexpectedly.", next: "Press Enter to try again.", commands: []string{"/marshal resume"}, verb: "try again"}
		if cause, ok := strings.CutPrefix(strings.TrimSpace(v.note), "stopped: "); ok {
			if at := approvalRequiredRE.FindStringIndex(cause); at != nil {
				s.reason = firstLine(cause[at[0]:])
			}
		}
		return s, true
	}
	return runStop{}, false
}

// approvalRequiredRE matches the governed build's Process 05 approval
// refusal (marshal_approval_wait_default.go). It stops execution instead of
// pausing the run, and its approval id is what /marshal approve-task needs,
// so the stop shows it instead of the generic sentence.
var approvalRequiredRE = regexp.MustCompile(`process 05: approval [A-Za-z0-9-]+ required`)

// legacyRetryRE finds the task an older build's resolution told the
// operator to retry ("… then /marshal retry T-3 and /marshal resume").
var legacyRetryRE = regexp.MustCompile(`/marshal retry ([A-Za-z0-9][A-Za-z0-9._-]*)`)

// legacyPauseAction is the action a pause without one (written before
// pauses carried an action) asks for, read from its resolution text.
func legacyPauseAction(next string) (marshal.PauseAction, string) {
	if m := legacyRetryRE.FindStringSubmatch(next); m != nil {
		return marshal.PauseRetryTask, m[1]
	}
	if strings.Contains(next, "/marshal use-plan") {
		return marshal.PauseUsePlan, ""
	}
	return marshal.PauseResume, ""
}

// pauseCommands is what Enter runs for a pause, from its action.
func pauseCommands(a marshal.PauseAction, task string) []string {
	switch a {
	case marshal.PauseRetryTask:
		return []string{"/marshal retry " + task, "/marshal resume"}
	case marshal.PauseUsePlan:
		return []string{"/marshal use-plan"}
	}
	return []string{"/marshal resume"}
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return cleanBoard(strings.TrimSpace(s))
}

func stopVerb(action marshal.PauseAction) string {
	switch action {
	case marshal.PauseRetryTask:
		return "retry the task"
	case marshal.PauseUsePlan:
		return "tell the Marshal what to change"
	default:
		return "continue"
	}
}

// layoutCounts is what the Board's panes hold, for the layout. A run's
// stop is the inbox's first entry, two rows as a request is.
func (v boardVisual) layoutCounts() layout.Counts {
	b := v.board
	inbox := len(b.Inbox) + len(v.gates)
	if _, stopped := v.stopNotice(); stopped {
		inbox++
	}
	return layout.Counts{Lanes: len(b.Lanes) + v.tailWant, Tasks: len(b.Tasks), Inbox: inbox, Activity: len(b.Activity)}
}

// stateChip is the header's run state in the owner's words.
func (v boardVisual) stateChip() string {
	word, color := v.runWord()
	return v.bold(v.fg(color, word))
}

// runWord is the run's state as the header says it, and its colour key.
func (v boardVisual) runWord() (string, string) {
	if s, ok := v.stopNotice(); ok {
		if s.waiting {
			return "waiting for you", "needs"
		}
		return "paused", "failed"
	}
	if v.ownerWaits() {
		// The Marshal waits for the owner's reply (board_gates.go), or
		// changed files wait for keep or undo (board_guard.go).
		return "waiting for you", "needs"
	}
	switch v.board.Run.State {
	case marshal.Closed:
		return "done", "merged"
	case marshal.Drafting:
		return "planning", "accent"
	case "":
		return "starting", "dim"
	}
	return "running", "accent"
}

// stopLine is the footer's one-line notice and its Enter action.
func (v boardVisual) stopLine(s runStop) string {
	color, word := "failed", "stopped"
	if s.waiting {
		color, word = "waiting", "waits for you"
	}
	if v.stopArmed {
		return v.fg("waiting", "Press Enter again to "+s.verb+". Any other key cancels.")
	}
	return v.fg(color, "▮ "+word) + v.fg("dim", " · ") + s.reason + v.fg("dim", " · Enter: ") + v.bold(s.verb)
}

// stopEntry is the stop as the inbox's first entry, with one Enter button.
func (v boardVisual) stopEntry(s runStop, x, row, w int) []string {
	color, what := "failed", " stopped · "
	if s.waiting {
		color, what = "waiting", " waits for you · "
	}
	flag := "⚑ "
	if !v.unicode() {
		flag = "! "
	}
	first := v.fg(color, flag+"run") + v.fg("dim", what) + v.bold(v.fg("white", s.reason))
	button := "[Enter]"
	second := "    " + v.bg("rule", button) + " " + s.verb + "   " + v.fg("dim", s.next)
	v.hit(boardHit{row: row + 1, x0: x + 4, x1: x + min(w, 4+VisibleLen(button)), kind: hitStop})
	return []string{PadCell(first, w), PadCell(second, w)}
}
