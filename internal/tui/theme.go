package tui

import (
	"fmt"
	"regexp"
	"strings"
)

// ThemeMode specifies the visual style mode.
type ThemeMode string

const (
	ThemeDefault      ThemeMode = "default"
	ThemeMonochrome   ThemeMode = "monochrome"
	ThemeHighContrast ThemeMode = "high-contrast"
	ThemeNoColor      ThemeMode = "no-color"
)

var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// StripANSI removes all ANSI escape sequences from a string.
func StripANSI(str string) string {
	return ansiRegex.ReplaceAllString(str, "")
}

// VisibleLen returns the printable rune length of a string ignoring ANSI codes.
func VisibleLen(str string) int {
	clean := StripANSI(str)
	return len([]rune(clean))
}

// Theme encapsulates colors and box drawing characters for the TUI.
type Theme struct {
	Mode             ThemeMode
	Unicode          bool
	AnimationEnabled bool

	// ANSI color codes
	Reset     string
	Bold      string
	Dim       string
	Underline string
	Reverse   string

	// Semantic colors
	Marshal  string // Cyan/Magenta for brand & ULTRA
	Ultra    string // Magenta
	Success  string // Green
	Warning  string // Yellow
	Danger   string // Red
	Accent   string // Purple / Contested / Challenge
	Muted    string // Gray
	Active   string // Bright Blue/Cyan
	Border   string // Subtle border color
	HeaderBg string // Inverted/header background

	// Box drawing symbols
	BoxTopLeft     string
	BoxTopRight    string
	BoxBottomLeft  string
	BoxBottomRight string
	BoxHoriz       string
	BoxVert        string
	BoxTDown       string
	BoxTUp         string
	BoxTRight      string
	BoxTLeft       string
	BoxCross       string

	// Glyphs
	GlyphCheck    string
	GlyphCross    string
	GlyphDotFull  string
	GlyphDotHalf  string
	GlyphDotEmpty string
	GlyphArrowR   string
	GlyphArrowD   string
	GlyphSpinner  []string
}

// NewTheme creates a Theme based on the specified mode, Unicode availability, and animation flag.
func NewTheme(mode ThemeMode, unicode bool, animation bool) *Theme {
	if mode == "" {
		mode = ThemeDefault
	}

	t := &Theme{
		Mode:             mode,
		Unicode:          unicode,
		AnimationEnabled: animation,
	}

	switch mode {
	case ThemeNoColor:
		t.Reset = ""
		t.Bold = ""
		t.Dim = ""
		t.Underline = ""
		t.Reverse = ""
		t.Marshal = ""
		t.Ultra = ""
		t.Success = ""
		t.Warning = ""
		t.Danger = ""
		t.Accent = ""
		t.Muted = ""
		t.Active = ""
		t.Border = ""
		t.HeaderBg = ""

	case ThemeMonochrome:
		t.Reset = "\x1b[0m"
		t.Bold = "\x1b[1m"
		t.Dim = "\x1b[2m"
		t.Underline = "\x1b[4m"
		t.Reverse = "\x1b[7m"
		t.Marshal = "\x1b[1m"
		t.Ultra = "\x1b[1m"
		t.Success = "\x1b[1m"
		t.Warning = "\x1b[4m"
		t.Danger = "\x1b[7m"
		t.Accent = "\x1b[1m"
		t.Muted = "\x1b[2m"
		t.Active = "\x1b[1m"
		t.Border = "\x1b[2m"
		t.HeaderBg = "\x1b[7m"

	case ThemeHighContrast:
		t.Reset = "\x1b[0m"
		t.Bold = "\x1b[1m"
		t.Dim = ""
		t.Underline = "\x1b[4m"
		t.Reverse = "\x1b[7m"
		t.Marshal = "\x1b[1;97;44m" // Bold white on blue
		t.Ultra = "\x1b[1;95m"      // Bright magenta
		t.Success = "\x1b[1;92m"    // Bright green
		t.Warning = "\x1b[1;93m"    // Bright yellow
		t.Danger = "\x1b[1;91m"     // Bright red
		t.Accent = "\x1b[1;96m"     // Bright cyan
		t.Muted = "\x1b[97m"       // Bright white
		t.Active = "\x1b[1;96m"     // Bright cyan
		t.Border = "\x1b[1;97m"     // Bright white
		t.HeaderBg = "\x1b[1;97;40m"

	default: // ThemeDefault
		t.Reset = "\x1b[0m"
		t.Bold = "\x1b[1m"
		t.Dim = "\x1b[2m"
		t.Underline = "\x1b[4m"
		t.Reverse = "\x1b[7m"
		t.Marshal = "\x1b[38;5;39m"  // Vivid cyan / blue
		t.Ultra = "\x1b[38;5;171m"   // Vivid magenta/purple
		t.Success = "\x1b[38;5;42m"  // Emerald green
		t.Warning = "\x1b[38;5;214m" // Warm amber
		t.Danger = "\x1b[38;5;196m"  // Coral red
		t.Accent = "\x1b[38;5;141m"  // Soft purple
		t.Muted = "\x1b[38;5;244m"   // Slate gray
		t.Active = "\x1b[38;5;75m"   // Sky blue
		t.Border = "\x1b[38;5;239m"  // Dark slate border
		t.HeaderBg = "\x1b[48;5;236m"
	}

	// Box characters & Glyphs
	if unicode {
		t.BoxTopLeft = "╭"
		t.BoxTopRight = "╮"
		t.BoxBottomLeft = "╰"
		t.BoxBottomRight = "╯"
		t.BoxHoriz = "─"
		t.BoxVert = "│"
		t.BoxTDown = "┬"
		t.BoxTUp = "┴"
		t.BoxTRight = "├"
		t.BoxTLeft = "┤"
		t.BoxCross = "┼"

		t.GlyphCheck = "✓"
		t.GlyphCross = "✗"
		t.GlyphDotFull = "●"
		t.GlyphDotHalf = "◐"
		t.GlyphDotEmpty = "○"
		t.GlyphArrowR = "►"
		t.GlyphArrowD = "▼"
		t.GlyphSpinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	} else {
		t.BoxTopLeft = "+"
		t.BoxTopRight = "+"
		t.BoxBottomLeft = "+"
		t.BoxBottomRight = "+"
		t.BoxHoriz = "-"
		t.BoxVert = "|"
		t.BoxTDown = "+"
		t.BoxTUp = "+"
		t.BoxTRight = "+"
		t.BoxTLeft = "+"
		t.BoxCross = "+"

		t.GlyphCheck = "v"
		t.GlyphCross = "x"
		t.GlyphDotFull = "*"
		t.GlyphDotHalf = "o"
		t.GlyphDotEmpty = "."
		t.GlyphArrowR = ">"
		t.GlyphArrowD = "v"
		t.GlyphSpinner = []string{"|", "/", "-", "\\"}
	}

	return t
}

// Colorize wraps text with color code and resets it.
func (t *Theme) Colorize(color, text string) string {
	if t.Mode == ThemeNoColor || color == "" {
		return text
	}
	return color + text + t.Reset
}

// RenderBadge renders a status badge with text and appropriate styling.
func (t *Theme) RenderBadge(state string) string {
	stateUpper := strings.ToUpper(strings.TrimSpace(state))
	switch stateUpper {
	case "VERIFIED", "SUCCESS", "PASS", "READY", "ACTIVE", "AUTHENTICATED", "AVAILABLE", "CLEAN":
		return t.Colorize(t.Success, fmt.Sprintf("%s %s", t.GlyphCheck, stateUpper))
	case "SUPPORTED", "VERIFYING", "WARNING", "PENDING", "THINKING", "PLANNING", "EXECUTING", "EDITING":
		return t.Colorize(t.Warning, fmt.Sprintf("%s %s", t.GlyphDotHalf, stateUpper))
	case "CONTESTED", "CHALLENGE", "HANDOFF":
		return t.Colorize(t.Accent, fmt.Sprintf("%s %s", t.GlyphArrowR, stateUpper))
	case "INVALIDATED", "BLOCKED", "FAILED", "CRITICAL", "BLOCKED_BY_POLICY", "CANCELLED":
		return t.Colorize(t.Danger, fmt.Sprintf("%s %s", t.GlyphCross, stateUpper))
	case "STALE", "UNSUPPORTED", "IDLE", "WAITING", "DONE", "PAUSED":
		return t.Colorize(t.Muted, fmt.Sprintf("%s %s", t.GlyphDotEmpty, stateUpper))
	case "UNAVAILABLE", "NOT_AVAILABLE", "NOT_RUN", "UNKNOWN", "UNRESOLVED":
		return t.Colorize(t.Muted, fmt.Sprintf("? %s", stateUpper))
	default:
		return t.Colorize(t.Muted, stateUpper)
	}
}

// Truncate ensures text fits within width, adding an ellipsis if needed.
func Truncate(str string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(str)
	if len(runes) <= width {
		return str
	}
	if width <= 1 {
		return string(runes[:width])
	}
	return string(runes[:width-1]) + "…"
}

// PadRight pads a string with spaces up to the requested visible width.
func PadRight(str string, width int) string {
	vLen := VisibleLen(str)
	if vLen >= width {
		return str
	}
	return str + strings.Repeat(" ", width-vLen)
}
