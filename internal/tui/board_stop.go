package tui

import (
	"errors"
	"regexp"
	"slices"
	"strings"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	"github.com/Zen1th53/marshal/internal/tui/board/model"
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

// cardEnterKey is the key an open card's Enter button names: Enter, or
// "click" while a stopped run leads the inbox and takes Enter itself
// (boardStopWaiting), so no card below it claims the key.
func (v boardVisual) cardEnterKey() string {
	if _, ok := v.stopNotice(); ok && len(v.board.Inbox) == 0 {
		return "click"
	}
	return "Enter"
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
		if reason, next, command, ok := parseKnownStopNote(v.note); ok {
			s.reason = reason
			if next != "" {
				s.next = next
			}
			s.commands = []string{command}
		}
		return s, true
	}
	return runStop{}, false
}

// knownStopPrefix marks a panel note that carries a stop's own plain reason
// and, after two spaces, what to do (the note stays one line wherever it is
// shown), so the stop shows them instead of the generic sentence. Only errors
// written for people carry one; a raw error stays behind "The run stopped
// unexpectedly." and goes to the log.
const knownStopPrefix = "stopped because: "

// knownCloseStopPrefix is knownStopPrefix for a stop of the person's own
// close (/marshal close): Enter closes again rather than resuming the run.
const knownCloseStopPrefix = "delivery stopped because: "

// knownStopGap separates a known stop's reason from what to do.
const knownStopGap = "  "

// knownStopNote is the panel note for an error that says why in plain words
// (app.DeliveryStopError), or "" for any other error.
func knownStopNote(err error) string { return knownStopNoteWith(knownStopPrefix, err) }

// knownCloseStopNote is knownStopNote for a failed /marshal close.
func knownCloseStopNote(err error) string { return knownStopNoteWith(knownCloseStopPrefix, err) }

func knownStopNoteWith(prefix string, err error) string {
	var known interface{ StopReason() (string, string) }
	if !errors.As(err, &known) {
		return ""
	}
	reason, next := known.StopReason()
	reason, next = strings.TrimSpace(reason), strings.TrimSpace(next)
	if reason == "" || strings.ContainsAny(reason+next, "\r\n") {
		return ""
	}
	reason = strings.ReplaceAll(reason, knownStopGap, " ")
	if next != "" {
		return prefix + reason + knownStopGap + next
	}
	return prefix + reason
}

// parseKnownStopNote reads a known stop's note back: its reason, what to do
// and the command Enter runs.
func parseKnownStopNote(note string) (string, string, string, bool) {
	note = strings.TrimSpace(note)
	command := "/marshal resume"
	rest, ok := strings.CutPrefix(note, knownStopPrefix)
	if !ok {
		if rest, ok = strings.CutPrefix(note, knownCloseStopPrefix); !ok {
			return "", "", "", false
		}
		command = "/marshal close"
	}
	reason, next, _ := strings.Cut(rest, knownStopGap)
	reason = cleanBoard(strings.TrimSpace(reason))
	if reason == "" {
		return "", "", "", false
	}
	return reason, cleanBoard(strings.TrimSpace(next)), command, true
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
// stop is the inbox's first entry, two rows as a request is. The open cards
// (what next, changed files, ask, requirements, change) are entries too:
// without them a Board beside the chat gives the inbox its one-row strip and
// the card is never seen. The requirements card is tall, so it asks for
// three entries' rows.
func (v boardVisual) layoutCounts() layout.Counts {
	b := v.board
	// Room for every entry the inbox lists: requests, gates (a sent one
	// still shows) and the open cards; the tall requirements card takes
	// two more entries' rows.
	inbox := len(b.Inbox) + len(v.gates) + v.cardCount() + 2*v.reqCount()
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

// stateChipShort is the state chip without its reason: the words before
// ": ", for a header with no room for the reason.
func (v boardVisual) stateChipShort() string {
	word, color := v.runWord()
	short, _, _ := strings.Cut(word, ": ")
	return v.bold(v.fg(color, short))
}

// runWord is the run's state as the header says it, and its colour key.
func (v boardVisual) runWord() (string, string) {
	if s, ok := v.stopNotice(); ok {
		if s.waiting {
			return "waiting for you", "needs"
		}
		return "paused", "failed"
	}
	if v.ownerWaitsForRun() && !(v.board.Run.State == marshal.Closed && v.onlyNextWaits()) {
		// The Marshal waits for the owner's reply (board_gates.go), or
		// changed files wait for keep or undo (board_guard.go). The "what
		// next?" card of a closed run is no wait: the run is done.
		return "waiting for you", "needs"
	}
	switch v.board.Run.State {
	case marshal.Reviewing:
		return "reviewing", "accent"
	case marshal.Approved, marshal.Dispatching:
		if v.onlyReviewing() {
			// Every task still moving is in the Marshal's review: no
			// worker runs, and nothing waits for the owner.
			return "reviewing", "accent"
		}
	case marshal.Closed:
		return "done", "merged"
	case marshal.Merging:
		return "merging", "accent"
	case marshal.Verifying:
		// The runtime's final check of the merged work runs here before the
		// run closes. An independent verifier re-runs the checks, which can
		// take minutes, so the header says so, and why, instead of
		// "running"; a narrow header keeps "final check" (stateChipShort).
		return "final check: re-running the checks on the merged result", "accent"
	case marshal.Drafting:
		return "planning", "accent"
	case "":
		return "starting", "dim"
	}
	return "running", "accent"
}

// ownerWaitsForRun is ownerWaits as the run's state reads it: a requirements
// card is settled before planning, so one still open while the run executes
// does not hold the run up, and the header says what the run is doing.
func (v boardVisual) ownerWaitsForRun() bool {
	if v.req != nil && slices.Contains(guardRunActive, v.board.Run.State) {
		u := v
		u.req = nil
		return u.ownerWaits()
	}
	return v.ownerWaits()
}

// onlyReviewing reports whether at least one task is in review and none is
// working: the run is reviewing.
func (v boardVisual) onlyReviewing() bool {
	review := 0
	for _, t := range v.board.Tasks {
		switch t.State {
		case model.TaskReview:
			review++
		case model.TaskWorking, "verifying":
			return false
		}
	}
	return review > 0 && v.liveLanes() == 0
}

// onlyNextWaits reports whether the "what next?" card is the only thing open
// for the owner.
func (v boardVisual) onlyNextWaits() bool {
	rest := v
	rest.next = nil
	return v.next != nil && !rest.ownerWaits()
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
