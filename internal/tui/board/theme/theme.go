// Package theme provides colors, styles, and glyph tables with ASCII fallbacks
// for the Board. Terminals differ; the Board must read the same in all of them.
package theme

import (
	"strings"
)

// ANSI escape codes used by the Board theme.
const (
	ansiReset     = "\x1b[0m"
	ansiBold      = "\x1b[1m"
	ansiDim       = "\x1b[2m"
	ansiReverse   = "\x1b[7m"
	ansiUnderline = "\x1b[4m"

	// Six state colours:
	// working blue, waiting yellow, parked orange, failed red, done green, verifying purple.
	ansiBlue   = "\x1b[38;5;39m"  // working
	ansiYellow = "\x1b[38;5;214m" // waiting / review
	ansiOrange = "\x1b[38;5;208m" // parked / rework
	ansiRed    = "\x1b[38;5;196m" // failed / cancelled / blocked
	ansiGreen  = "\x1b[38;5;42m"  // done / merged / accepted
	ansiPurple = "\x1b[38;5;141m" // verifying

	// Selection background (reverse or 256-color bg)
	ansiSelectionBg = "\x1b[7m"
)

// State names the lifecycle / health state of tasks, lanes, and inbox items.
type State string

const (
	StateWorking   State = "working"
	StateWaiting   State = "waiting"
	StateParked    State = "parked"
	StateFailed    State = "failed"
	StateDone      State = "done"
	StateVerifying State = "verifying"
)

// Glyphs holds the set of symbols used on the Board, adapting between
// UTF-8 and plain ASCII.
type Glyphs struct {
	BrailleSpinner []string
	ProbeSpinner   []string
	WIP            string
	Bullet         string
	Check          string
	Cross          string
	HalfDot        string
	EmptyDot       string
	ArrowRight     string
	ArrowDown      string
	BoxVert        string
	BoxHoriz       string
}

// UnicodeGlyphs is the full UTF-8 glyph table.
var UnicodeGlyphs = Glyphs{
	BrailleSpinner: []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"},
	ProbeSpinner:   []string{"◐", "◓", "◑", "◒"},
	WIP:            "[WIP]",
	Bullet:         "●",
	Check:          "✓",
	Cross:          "✗",
	HalfDot:        "◐",
	EmptyDot:       "○",
	ArrowRight:     "►",
	ArrowDown:      "▼",
	BoxVert:        "│",
	BoxHoriz:       "─",
}

// ASCIIGlyphs is the ASCII fallback glyph table.
var ASCIIGlyphs = Glyphs{
	BrailleSpinner: []string{"|", "/", "-", "\\"},
	ProbeSpinner:   []string{"|", "/", "-", "\\"},
	WIP:            "[WIP]",
	Bullet:         "*",
	Check:          "v",
	Cross:          "x",
	HalfDot:        "o",
	EmptyDot:       ".",
	ArrowRight:     ">",
	ArrowDown:      "v",
	BoxVert:        "|",
	BoxHoriz:       "-",
}

// Theme encapsulates colors, attributes, and glyphs for the Board.
type Theme struct {
	NoColor bool
	Unicode bool

	// Six state colours:
	Working   string
	Waiting   string
	Parked    string
	Failed    string
	Done      string
	Verifying string

	// Common styling:
	Selection string
	Dim       string
	Bold      string
	Reset     string

	// Glyphs:
	Glyphs Glyphs
}

// New creates a Board theme given noColor and unicode flags.
// When noColor is true, all color and style escape codes are empty strings.
// When unicode is false, ASCII fallback glyphs are used.
func New(noColor, unicode bool) *Theme {
	t := &Theme{
		NoColor: noColor,
		Unicode: unicode,
	}

	if unicode {
		t.Glyphs = UnicodeGlyphs
	} else {
		t.Glyphs = ASCIIGlyphs
	}

	if noColor {
		// Monochrome / NO_COLOR theme: zero escape codes.
		return t
	}

	// Colour enabled:
	t.Reset = ansiReset
	t.Bold = ansiBold
	t.Dim = ansiDim
	t.Selection = ansiSelectionBg

	t.Working = ansiBlue
	t.Waiting = ansiYellow
	t.Parked = ansiOrange
	t.Failed = ansiRed
	t.Done = ansiGreen
	t.Verifying = ansiPurple

	return t
}

// Colorize wraps text with the given escape code and resets it, unless NoColor is active.
func (t *Theme) Colorize(code, text string) string {
	if t.NoColor || code == "" || text == "" {
		return text
	}
	return code + text + t.Reset
}

// StateColor returns the colour code for a given Board state.
func (t *Theme) StateColor(s State) string {
	switch s {
	case StateWorking:
		return t.Working
	case StateWaiting:
		return t.Waiting
	case StateParked:
		return t.Parked
	case StateFailed:
		return t.Failed
	case StateDone:
		return t.Done
	case StateVerifying:
		return t.Verifying
	default:
		return ""
	}
}

// StateWord returns a text representation of the state that is distinguishable
// without colour.
func StateWord(s State) string {
	return strings.ToUpper(string(s))
}

// GlyphFallback replaces Unicode glyphs with their ASCII equivalents.
func GlyphFallback(s string) string {
	r := strings.NewReplacer(
		"🚧", "[WIP]",
		"●", "*",
		"◐", "o",
		"○", ".",
		"✓", "v",
		"✗", "x",
		"►", ">",
		"▼", "v",
		"│", "|",
		"─", "-",
		"⣾", "|", "⣽", "/", "⣻", "-", "⢿", "\\",
		"⡿", "|", "⣟", "/", "⣯", "-", "⣷", "\\",
		"◓", "/", "◑", "-", "◒", "\\",
	)
	return r.Replace(s)
}
