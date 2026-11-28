package tui

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/Zen1th53/marshal/internal/board/vm"
)

// dockStrip is what the Board v2 MARSHAL CHAT strip says, taken from one
// frame of the v2 view model: the chat pane's tmux title carries it, since
// the chat itself is a tmux pane the Board cannot draw over. Empty strings
// and a false known mean the collector has not said; that part is left out
// of the title rather than shown as a dash.
type dockStrip struct {
	provider string
	model    string
	effort   string
	known    bool // the chat collector filled the chat's state
	bound    bool
	clear    bool // input clear
	// inputKnown is set once the input box was looked at; until then the
	// strip says nothing about the input.
	inputKnown bool
	stage      string
	answered   int
}

// dockStripOf reads the strip's fields out of a v2 frame.
func dockStripOf(b vm.Board) dockStrip {
	s := dockStrip{
		provider: dockTitleText(b.Marshal.Provider),
		model:    dockStripText(b.Marshal.Model),
		effort:   dockTitleText(b.Marshal.Effort),
		stage:    dockStripText(b.Chat.Stage),
		answered: b.Chat.Answered,
	}
	if s.stage == "live" || s.stage == "not bound" {
		// The binding mark already says it.
		s.stage = ""
	}
	s.known = b.Chat != (vm.Chat{}) || b.Marshal.ChatBound || b.Marshal.InputClear || len(b.Requests) > 0
	s.bound = b.Chat.Bound || b.Marshal.ChatBound
	s.inputKnown = b.Chat.InputKnown || b.Marshal.InputKnown
	s.clear = s.inputKnown && (b.Chat.InputClear || b.Marshal.InputClear)
	if s.answered == 0 {
		for _, r := range b.Requests {
			if r.Answer == "allow" || r.Answer == "decline" {
				s.answered++
			}
		}
	}
	return s
}

// dockStripNow is the strip for the chat's title now. It builds a v2 frame,
// so the caller must hold neither w.mu nor dock.mu: the collectors read the
// workspace through GetUIState.
func (w *Workspace) dockStripNow() dockStrip {
	return dockStripOf(w.boardV2())
}

// noteDockStrip is called with each painted v2 frame: when what the strip
// says has changed, the dock is kicked so the chat's title follows. The kick
// runs on its own goroutine so the paint never waits on dock.mu.
func (w *Workspace) noteDockStrip(b vm.Board) {
	if !w.dock.docked.Load() {
		return
	}
	s := dockStripOf(b)
	if old := w.dock.strip.Load(); old != nil && *old == s {
		return
	}
	w.dock.strip.Store(&s)
	go w.kickDock()
}

// dockStripText keeps a free-text field safe for a tmux title: printable,
// no '#' (tmux formats), at most 40 runes, trimmed.
func dockStripText(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if n >= 40 {
			break
		}
		if r == '#' || !unicode.IsPrint(r) {
			continue
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}

// The parts of the strip that give way, in the order they go.
const (
	stripTail     = iota // "  F11 Board"
	stripAnswered        // "3 requests answered"
	stripInput           // "input clear"
	stripStage           // "│ stage tasks · 6m"
	stripLabel           // "bound to run "
	stripProvider        // "claude" after the glyph
	stripEffort          // "· medium"
	stripSwitch          // " switch" after [m]
	stripModel           // "· opus-5.5"
	stripBrand           // "MARSHAL CHAT"
	stripBound           // "● live"
	stripParts
)

// dockStripTitle lays the chat's title out as the v2 strip:
//
//	MARSHAL CHAT  ✳ claude · opus-5.5 · medium  │ bound to run ● live  │ stage tasks · 6m    3 requests answered · input clear   [m] switch  F11 Board
//
// A part with nothing to say is left out. Too wide, the parts give way from
// the right in the order of the strip* constants ("F11 Board", the requests
// answered, input clear, the stage, the "bound to run" label, the provider
// name, the effort, " switch", the model name, the brand, the binding); then
// the most important of those that went come back while they still fit. The
// glyph and [m] are never dropped; tmux cuts what is still too wide, and the
// title is always one row.
func dockStripTitle(s dockStrip, unicode bool, width int, pad bool) (string, int) {
	sep, bar, live, unbound := " · ", "│", "●", "○"
	if !unicode {
		sep, bar, live, unbound = " . ", "|", "*", "o"
	}
	fix := func(t string) string {
		if !unicode {
			t = strings.ReplaceAll(t, "·", ".")
		}
		return t
	}
	name := s.provider
	if name == "" {
		name = "chat"
	}
	glyph := providerGlyph(name, unicode)
	provider, model, effort, stage := name, fix(s.model), s.effort, fix(s.stage)
	if glyph == "" {
		// The provider name is the only mark there is; it stays.
		glyph, provider = name, ""
	}
	answered := ""
	if s.answered > 0 {
		answered = strconv.Itoa(s.answered) + " requests answered"
		if s.answered == 1 {
			answered = "1 request answered"
		}
	}
	input := ""
	if s.known && s.inputKnown {
		input = "input waiting"
		if s.clear {
			input = "input clear"
		}
	}
	var off [stripParts]bool
	use := func(k int, v string) string {
		if off[k] {
			return ""
		}
		return v
	}
	build := func() (string, int) {
		head := " "
		if !off[stripBrand] {
			head = " MARSHAL CHAT  "
		}
		head += glyph
		first := true
		for _, p := range []string{use(stripProvider, provider), use(stripModel, model), use(stripEffort, effort)} {
			if p == "" {
				continue
			}
			if first {
				head += " " + p
			} else {
				head += sep + p
			}
			first = false
		}
		if s.known && !off[stripBound] {
			mark := live + " live"
			if !s.bound {
				mark = unbound + " not bound"
			}
			if off[stripLabel] {
				head += "  " + mark
			} else {
				head += "  " + bar + " bound to run " + mark
			}
		}
		if st := use(stripStage, stage); st != "" {
			head += "  " + bar + " stage " + st
		}
		info := use(stripAnswered, answered)
		if in := use(stripInput, input); in != "" {
			if info != "" {
				info += sep
			}
			info += in
		}
		keys := "[m]" + use(stripSwitch, " switch") + use(stripTail, "  F11 Board") + " "
		right := keys
		if info != "" {
			right = info + "   " + keys
		}
		gap := 4
		if pad && width > 0 {
			gap = max(2, width-dockMarkerW-VisibleLen(head)-VisibleLen(right))
		}
		at := VisibleLen(head) + gap + VisibleLen(right) - VisibleLen(keys)
		return head + strings.Repeat(" ", gap) + right, at
	}
	title, at := build()
	if width <= 0 {
		return title, at
	}
	avail := width - dockMarkerW
	fits := func() bool {
		t, _ := build()
		return VisibleLen(t) <= avail
	}
	var gone []int
	for k := 0; k < stripParts && !fits(); k++ {
		off[k] = true
		gone = append(gone, k)
	}
	for i := len(gone) - 1; i >= 0; i-- {
		off[gone[i]] = false
		if !fits() {
			off[gone[i]] = true
		}
	}
	return build()
}
