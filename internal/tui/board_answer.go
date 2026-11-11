package tui

import (
	"fmt"
	"strings"
)

// The Board's footer has room for one line, so a command answer of several
// lines (/status, /goal, /tasks, a usage text) opens in an overlay over the
// Board instead of showing only its last line. Like the help it scrolls
// with j/k and the page keys, and Esc or Enter closes it. Any other key
// closes it and goes on to do what it does, so the next command is typed
// whole; a click or a newer run note closes it too, the way they replace
// the footer's one-line answer. Background messages never open it: they
// are Activity lines (board_notes.go).

// boardAnswerText is the command answer the Board has not shown yet, when
// it has more than one line; "" otherwise.
// Only an answer to the operator's own command counts: a background
// message (RecordActivity) changes neither the answer nor its number.
func (w *Workspace) boardAnswerText() string {
	w.mu.RLock()
	out := w.answer
	w.mu.RUnlock()
	seq := w.outputSeq.Load()
	if seq == w.boardUI.ackSeq || !multiLine(out) {
		return ""
	}
	return out
}

func multiLine(s string) bool {
	return strings.Contains(strings.TrimSpace(s), "\n")
}

// handleBoardAnswerKey is the answer overlay's keys while it is open.
func (w *Workspace) handleBoardAnswerKey(event KeyEvent) bool {
	if !w.boardVisible() || w.diffViewer.IsOpen() || w.palette.IsOpen() || w.completionOpen || w.composing() || w.inboxView.Confirming() {
		return false
	}
	out := w.boardAnswerText()
	if out == "" {
		return false
	}
	ui := &w.boardUI
	switch {
	case event.Type == KeyDown || event.Type == KeyRune && event.Rune == 'j':
		ui.answerScroll = min(ui.answerMax, ui.answerScroll+1)
	case event.Type == KeyUp || event.Type == KeyRune && event.Rune == 'k':
		ui.answerScroll = max(0, ui.answerScroll-1)
	case event.Type == KeyPgDn:
		ui.answerScroll = min(ui.answerMax, ui.answerScroll+10)
	case event.Type == KeyPgUp:
		ui.answerScroll = max(0, ui.answerScroll-10)
	case event.Type == KeyEsc || event.Type == KeyEnter:
		ui.ack, ui.ackSeq, ui.answerScroll = out, w.outputSeq.Load(), 0
	default:
		// Any other key closes the answer and still does what it does:
		// typing the next command starts with its first character.
		ui.ack, ui.ackSeq, ui.answerScroll = out, w.outputSeq.Load(), 0
		return false
	}
	return true
}

// answerOverlay boxes a command answer w cells wide and h rows high,
// scrolled down by scroll lines, and returns how far it can scroll.
func (v boardVisual) answerOverlay(text string, isError bool, w, h, scroll int) ([]string, int) {
	th := v.theme
	if th == nil {
		th = NewTheme(ThemeDefault, true, true)
	}
	inner := max(1, w-4)
	var body []string
	for _, l := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		l = cleanBoard(strings.ReplaceAll(l, "\t", "    "))
		for VisibleLen(l) > inner {
			cut := cellRange(l, 0, inner)
			body = append(body, cut)
			l = cellRange(l, inner, VisibleLen(l))
		}
		body = append(body, l)
	}
	rows := max(1, h-2)
	maxScroll := max(0, len(body)-rows)
	scroll = min(max(0, scroll), maxScroll)
	body = body[scroll:min(len(body), scroll+rows)]
	hint := "Esc closes"
	if maxScroll > 0 {
		hint = fmt.Sprintf("j/k scroll · %d more lines · Esc closes", maxScroll-scroll)
	}
	title := " ANSWER "
	color := "white"
	if isError {
		color = "failed"
	}
	out := []string{th.BoxTopLeft + th.BoxHoriz + v.bold(v.fg(color, title)) + strings.Repeat(th.BoxHoriz, max(0, w-3-VisibleLen(title))) + th.BoxTopRight}
	for _, l := range body {
		out = append(out, th.BoxVert+" "+PadCell(l, inner)+" "+th.BoxVert)
	}
	for len(out) < h-1 {
		out = append(out, th.BoxVert+strings.Repeat(" ", max(0, w-2))+th.BoxVert)
	}
	out = append(out, th.BoxBottomLeft+th.BoxHoriz+v.fg("dim", " "+hint+" ")+strings.Repeat(th.BoxHoriz, max(0, w-5-VisibleLen(hint)))+th.BoxBottomRight)
	for i := range out {
		out[i] = PadCell(out[i], w)
	}
	if !v.unicode() {
		out = hostASCII(boardASCIIGlyphs(out), w)
	}
	return out, maxScroll
}
