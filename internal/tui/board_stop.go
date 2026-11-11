package tui

import (
	"regexp"
	"slices"
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
}

// stopCommandRE finds the operator commands a pause resolution names.
var stopCommandRE = regexp.MustCompile(`/marshal [a-z][a-z-]*`)

// defaultStopNext is what resolves a pause that recorded no resolution:
// the same advice the run's note gives (marshalOutcomeNote).
const defaultStopNext = "see the escalated task, then /marshal amend or /marshal resume"

// stopNotice is the run's stop, if it stopped.
func (v boardVisual) stopNotice() (runStop, bool) {
	run := v.board.Run
	if run.State == marshal.AwaitingUser {
		s := runStop{waiting: true, reason: firstLine(run.Paused), next: firstLine(strings.Join(run.Next, "; ")), code: run.PauseCode}
		if s.reason == "" {
			s.reason = "the run needs your decision"
		}
		if s.next == "" {
			s.next = defaultStopNext
		}
		if run.PauseAction != "" {
			s.commands = pauseCommands(run.PauseAction, run.PauseTask)
		} else {
			s.commands = stopCommands(s.next) // paused by an older build
		}
		return s, true
	}
	note := strings.TrimSpace(v.note)
	if v.stopped && run.State != marshal.Closed {
		reason := strings.TrimPrefix(note, "stopped: ")
		s := runStop{reason: firstLine(reason), next: "fix the cause above, then /marshal resume", commands: []string{"/marshal resume"}}
		if s.reason == "" {
			s.reason = "the run's execution ended"
		}
		return s, true
	}
	return runStop{}, false
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

// stopCommands is the distinct commands text names, "/marshal resume"
// when it names none.
func stopCommands(text string) []string {
	var out []string
	for _, c := range stopCommandRE.FindAllString(text, -1) {
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		out = []string{"/marshal resume"}
	}
	return out
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
			return "waiting for you", "waiting"
		}
		return "paused", "failed"
	}
	if v.ownerWaits() {
		// The Marshal waits for the owner's reply (board_gates.go), or
		// changed files wait for keep or undo (board_guard.go).
		return "waiting for you", "waiting"
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

// stopLine is the footer's one-line notice: why, then the command to type.
func (v boardVisual) stopLine(s runStop) string {
	color, word := "failed", "run stopped: "
	if s.waiting {
		color, word = "waiting", "run waits for you: "
	}
	// The command comes first: a narrow footer cuts the reason, which the
	// inbox's entry shows whole, never what to type.
	return v.fg(color, "▮ "+strings.TrimSuffix(word, ": ")) + v.fg("dim", " · type ") + v.bold(strings.Join(s.commands, " or ")) + v.fg("dim", " · ") + s.reason
}

// stopEntry is the stop as the inbox's first entry, two rows: what stopped
// and why, then a button per command (a click types it into the composer,
// never runs it) and the run's own advice. x is the inbox's first cell,
// row its frame row.
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
	second, at := "    ", 4
	for i, c := range s.commands {
		if i > 0 {
			second += v.fg("dim", " or ")
			at += 4
		}
		second += v.fg("dim", "type › ") + v.bg("rule", v.fg("white", c))
		v.hit(boardHit{row: row + 1, x0: x + at, x1: x + min(w, at+7+VisibleLen(c)), kind: hitCommand, id: c})
		at += 7 + VisibleLen(c)
	}
	if rest := strings.TrimSpace(s.next); rest != "" && rest != strings.Join(s.commands, " or ") {
		second += v.fg("dim", "  "+rest)
	}
	return []string{PadCell(first, w), PadCell(second, w)}
}
