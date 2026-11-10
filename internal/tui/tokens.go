package tui

import (
	"strconv"
	"strings"
)

// Board design tokens: every colour and provider glyph the Board, the
// vitals row, the chat pane's title and the Providers view draw with.
// Colours carry meaning: green, amber and red are state; needs (Marshal
// orange) is only "the owner must act"; the rest is chrome.

var boardHex = map[string]string{"bg": "0b0e14", "panel": "0f131b", "band": "161b26", "sel": "1d2433", "rule": "262d3c", "fg": "c9d1e3", "dim": "6b7489", "faint": "3b4356", "accent": "8fb3ff", "working": "6fa8ff", "waiting": "e6c35c", "failed": "f2677a", "merged": "7fd69a", "verify": "c39bff", "white": "ffffff", "needs": "f0943f", "ink": "000000"}
var board256 = map[string]int{"bg": 233, "panel": 233, "band": 235, "sel": 237, "rule": 239, "fg": 252, "dim": 243, "faint": 238, "accent": 111, "working": 111, "waiting": 179, "failed": 203, "merged": 114, "verify": 141, "white": 15, "needs": 215, "ink": 16}

// providerMarks is each provider's glyph, and its ASCII form.
var providerMarks = map[string][2]string{
	"claude": {"✳", "cl"}, "codex": {"⬡", "cx"}, "agy": {"◆", "ag"}, "opencode": {"●", "oc"},
}

// providerOf is the provider a worker id or store name stands for:
// "codex-2" → "codex", "claude-code" → "claude", "antigravity" → "agy".
func providerOf(s string) string {
	switch s {
	case "claude-code":
		return "claude"
	case "antigravity":
		return "agy"
	}
	p, _, _ := strings.Cut(s, "-")
	if _, ok := providerMarks[p]; ok {
		return p
	}
	return ""
}

// providerGlyph is p's glyph, "" for a provider without one.
func providerGlyph(p string, unicode bool) string {
	m, ok := providerMarks[p]
	if !ok {
		return ""
	}
	if !unicode {
		return m[1]
	}
	return m[0]
}

// mark is p's glyph in colour, followed by a space; "" when p has none.
func (v boardVisual) mark(p, color string) string {
	g := providerGlyph(p, v.unicode())
	if g == "" {
		return ""
	}
	if color == "" {
		return g + " "
	}
	return v.fg(color, g) + " "
}

// tmuxColour is a palette token as a tmux colour name ("colour" + index).
func tmuxColour(name string) string { return "colour" + strconv.Itoa(board256[name]) }
