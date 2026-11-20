// Package v2 paints the Board v2 screens from a vm.Board. It is pure: it
// reads the view model and the terminal size and returns lines; it never
// reaches the store, the runtime or tmux. Every screen is drawn on a cell
// canvas first, then encoded for the terminal's colour mode, so the same
// layout serves truecolor, 256 colours, monochrome and no colour, and the
// ASCII mode only swaps glyphs.
package v2

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ColorMode is how the terminal takes colour.
type ColorMode int

const (
	ColorTrue ColorMode = iota // 24-bit
	Color256                   // xterm 256-colour palette
	ColorMono                  // bold only
	ColorNone                  // plain text
)

// Theme is what the renderer needs from the terminal: its colour mode and
// whether it draws Unicode box and block characters.
type Theme struct {
	Color   ColorMode
	Unicode bool
}

// Palette: the design's colours (docs/design/board-v2/tuipaint.py, C).
const (
	cInk   = "#e6e9ee"
	cDim   = "#8b96a8"
	cFaint = "#5a6577"
	cAcc   = "#6cb8ff"
	cOK    = "#5fd39a"
	cWarn  = "#f0c062"
	cBad   = "#ff6f7a"
	cVio   = "#b79cff"
	cRule  = "#2e3848"
	cBg    = "#0e1218"
	cTop   = "#172131"
	cNow   = "#13263a"
	cYouOK = "#0f2a1d"
	cYouW  = "#2e2410"
	cSel   = "#152a40"
	cModal = "#141d2a"
	cDark  = "#06121f"

	cSeg     = "#1d2a3d" // Marshal segment of the top bar
	cSegKey  = "#2a3a52" // its m key
	cTag     = "#18212d" // tag chips
	cKey     = "#222c3a" // key caps
	cButton  = "#1a2230" // secondary buttons
	cWarnInk = "#2a1c00" // ink on a warning chip
	cStrip   = "#101b2a" // MARSHAL CHAT strip
	cDimInk  = "#3a4352" // ink under an overlay
	cDimBg   = "#11161d" // panel ground under an overlay
	cPink    = "#e8807e"
)

type cell struct {
	r      rune
	fg, bg string
	b      bool
}

type canvas struct {
	w, h    int
	cells   [][]cell
	unicode bool
	hits    []Hit
}

// Hit is one clickable region of a painted screen: frame row Row and the
// cells [X0, X1), both from zero. Kind says what it stands for and Index
// which item of that part of the view model:
//
//	HitKey     b.Keys (or the screen's default keys) on the key bar
//	HitAction  a button of the card shown: Decision.Actions, Paused.Actions
//	HitReady   a button under the readiness checks: Ready.Actions
//	HitCheck   a readiness row: Ready.Checks
//	HitLane    a lane row: Lanes.Items
//	HitTask    a task row: Tasks
//	HitInbox   an inbox line: Inbox.Lines
type Hit struct {
	Row, X0, X1 int
	Kind        string
	Index       int
	Key         string // HitKey, HitAction, HitReady: the key the button names
}

// Hit kinds.
const (
	HitKey    = "key"
	HitAction = "action"
	HitReady  = "ready"
	HitCheck  = "check"
	HitLane   = "lane"
	HitTask   = "task"
	HitInbox  = "inbox"
)

// hit records a region, clipped to the canvas.
func (c *canvas) hit(y, x0, x1 int, kind string, index int, key string) {
	x0, x1 = max(0, x0), min(c.w, x1)
	if y < 0 || y >= c.h || x1 <= x0 {
		return
	}
	c.hits = append(c.hits, Hit{Row: y, X0: x0, X1: x1, Kind: kind, Index: index, Key: key})
}

func newCanvas(w, h int, unicode bool) *canvas {
	c := &canvas{w: w, h: h, unicode: unicode}
	c.cells = make([][]cell, h)
	for y := range c.cells {
		row := make([]cell, w)
		for x := range row {
			row[x] = cell{' ', cInk, cBg, false}
		}
		c.cells[y] = row
	}
	return c
}

// st is a style: foreground, optional background, bold.
type st struct {
	fg, bg string
	b      bool
}

// seg is one styled run of text.
type seg struct {
	t string
	s st
}

// putClip writes text at x,y with no cell past maxX (exclusive). It returns
// the column after the text, as if nothing was clipped.
func (c *canvas) putClip(x, y int, text string, s st, maxX int) int {
	if !c.unicode {
		text = ascii(text)
	}
	if maxX > c.w {
		maxX = c.w
	}
	fg := s.fg
	if fg == "" {
		fg = cInk
	}
	for _, r := range text {
		if x >= 0 && x < maxX && y >= 0 && y < c.h {
			cl := &c.cells[y][x]
			cl.r, cl.fg, cl.b = r, fg, s.b
			if s.bg != "" {
				cl.bg = s.bg
			}
		}
		x++
	}
	return x
}

func (c *canvas) put(x, y int, text string, s st) int { return c.putClip(x, y, text, s, c.w) }

func (c *canvas) segsClip(x, y int, parts []seg, maxX int) int {
	for _, p := range parts {
		x = c.putClip(x, y, p.t, p.s, maxX)
	}
	return x
}

func (c *canvas) fill(x, y, w, h int, bg string) {
	for yy := y; yy < y+h; yy++ {
		for xx := x; xx < x+w; xx++ {
			if xx >= 0 && xx < c.w && yy >= 0 && yy < c.h {
				c.cells[yy][xx] = cell{' ', cInk, bg, false}
			}
		}
	}
}

// dim greys the whole canvas, as under an overlay card. Nothing under the
// card stays clickable.
func (c *canvas) dim() {
	c.hits = nil
	for y := range c.cells {
		for x := range c.cells[y] {
			cl := &c.cells[y][x]
			cl.fg, cl.b = cDimInk, false
			if cl.bg != cBg {
				cl.bg = cDimBg
			}
		}
	}
}

// pane is a titled rounded box on the canvas. Its put methods take
// coordinates relative to the box's top-left corner and clip to the inside.
type pane struct {
	c          *canvas
	x, y, w, h int
}

// box draws ╭─ TITLE · detail ──── right ─╮. The part of the title before
// the first "·" is upper-case bold, the rest dim; right is faint.
func (c *canvas) box(x, y, w, h int, title, right, color, tcolor, bg string) pane {
	if color == "" {
		color = cRule
	}
	if bg != "" {
		c.fill(x, y, w, h, bg)
	}
	tl, tr, bl, br, hz, vt := "╭", "╮", "╰", "╯", "─", "│"
	if w >= 2 && h >= 2 {
		c.put(x, y, tl+strings.Repeat(hz, w-2)+tr, st{fg: color, bg: bg})
		for yy := y + 1; yy < y+h-1; yy++ {
			c.put(x, yy, vt, st{fg: color, bg: bg})
			c.put(x+w-1, yy, vt, st{fg: color, bg: bg})
		}
		c.put(x, y+h-1, bl+strings.Repeat(hz, w-2)+br, st{fg: color, bg: bg})
	}
	maxX := x + w - 1
	if title != "" {
		if tcolor == "" {
			tcolor = cInk
		}
		c.putClip(x+2, y, " ", st{fg: color, bg: bg}, maxX)
		xx := x + 3
		head, detail, has := strings.Cut(title, "·")
		xx = c.putClip(xx, y, strings.ToUpper(strings.TrimSpace(head)), st{fg: tcolor, bg: bg, b: true}, maxX)
		if has {
			xx = c.putClip(xx, y, " · "+strings.TrimSpace(detail), st{fg: cDim, bg: bg}, maxX)
		}
		c.putClip(xx, y, " ", st{fg: color, bg: bg}, maxX)
		if right != "" {
			rx := x + w - 3 - width(right) - 1
			if rx > xx+1 {
				c.putClip(rx, y, " "+right+" ", st{fg: cFaint, bg: bg}, maxX)
			}
		}
	} else if right != "" {
		c.putClip(x+w-3-width(right)-1, y, " "+right+" ", st{fg: cFaint, bg: bg}, maxX)
	}
	return pane{c, x, y, w, h}
}

func (p pane) put(dx, dy int, text string, s st) int {
	if dy <= 0 || dy >= p.h-1 {
		return p.x + dx
	}
	return p.c.putClip(p.x+dx, p.y+dy, text, s, p.x+p.w-1) - p.x
}

func (p pane) segs(dx, dy int, parts []seg) int {
	for _, s := range parts {
		dx = p.put(dx, dy, s.t, s.s)
	}
	return dx
}

func (p pane) fillRow(dx, dy, w int, bg string) {
	if dy <= 0 || dy >= p.h-1 {
		return
	}
	end := min(p.x+dx+w, p.x+p.w-1)
	p.c.fill(p.x+dx, p.y+dy, end-(p.x+dx), 1, bg)
}

// hit records a region of the box's inside from dx to endX (box-relative,
// exclusive) on row dy; a row outside the box's inside records nothing.
func (p pane) hit(dx, dy, endX int, kind string, index int, key string) {
	if dy <= 0 || dy >= p.h-1 {
		return
	}
	p.c.hit(p.y+dy, p.x+max(1, dx), p.x+min(endX, p.w-1), kind, index, key)
}

// rows is the number of rows inside the box.
func (p pane) rows() int { return max(0, p.h-2) }

// width counts cells; every glyph the Board uses is one cell wide.
func width(s string) int { return utf8.RuneCountInString(s) }

func ljust(s string, n int) string {
	if w := width(s); w < n {
		return s + strings.Repeat(" ", n-w)
	}
	return s
}

func rjust(s string, n int) string {
	if w := width(s); w < n {
		return strings.Repeat(" ", n-w) + s
	}
	return s
}

func cut(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if width(s) <= n {
		return s
	}
	r := []rune(s)
	if n == 1 {
		return string(r[:1])
	}
	return string(r[:n-1]) + "…"
}

// fit pads or cuts s to exactly n cells.
func fit(s string, n int) string { return ljust(cut(s, n), n) }

// asciiMap is how a glyph reads with no Unicode.
var asciiMap = map[rune]string{
	'╭': "+", '╮': "+", '╰': "+", '╯': "+", '─': "-", '│': "|", '┄': "-", '┆': ":",
	'█': "#", '░': ".", '▁': "_", '▂': "_", '▃': ".", '▄': "-", '▅': "=", '▆': "=", '▇': "#",
	'✓': "v", '✗': "x", '✳': "*", '◆': "+", '⬡': "o", '◇': "o", '⣾': "*", '◎': "o", '●': "*",
	'→': ">", '←': "<", '▶': ">", '◀': "<", '›': ">", '·': ".", '…': "...", '×': "x", '≈': "~",
	'⚠': "!", '↓': "v", '⏎': "Enter", '–': "-", '—': "-", '⎿': "L", '❯': ">", '⏵': ">", '✻': "*",
	'▐': "#", '▛': "#", '▜': "#", '▌': "#", '▝': "#", '▘': "#",
}

func ascii(s string) string {
	ok := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			ok = false
			break
		}
	}
	if ok {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if r < 0x80 {
			b.WriteRune(r)
		} else if m, found := asciiMap[r]; found {
			b.WriteString(m)
		} else {
			b.WriteByte('?')
		}
	}
	return b.String()
}

// lines encodes the canvas for the theme's colour mode.
func (c *canvas) lines(th Theme) []string {
	out := make([]string, c.h)
	for y, row := range c.cells {
		var b strings.Builder
		var prev st
		first := true
		for _, cl := range row {
			s := st{cl.fg, cl.bg, cl.b}
			if first || s != prev {
				b.WriteString(code(s, th.Color))
				prev, first = s, false
			}
			b.WriteRune(cl.r)
		}
		if th.Color != ColorNone {
			b.WriteString("\x1b[0m")
		}
		out[y] = b.String()
	}
	return out
}

func code(s st, mode ColorMode) string {
	bold := ""
	if s.b {
		bold = "1;"
	}
	switch mode {
	case ColorTrue:
		fr, fg, fb := rgb(s.fg)
		br, bgc, bb := rgb(s.bg)
		return fmt.Sprintf("\x1b[0;%s38;2;%d;%d;%d;48;2;%d;%d;%dm", bold, fr, fg, fb, br, bgc, bb)
	case Color256:
		return "\x1b[0;" + bold + "38;5;" + strconv.Itoa(xterm256(s.fg)) + ";48;5;" + strconv.Itoa(xterm256(s.bg)) + "m"
	case ColorMono:
		rev := ""
		if s.bg != cBg && s.bg != cTop && s.bg != cStrip && s.bg != cModal && s.bg != cDimBg && s.bg != cSel {
			rev = ";7"
		}
		if s.b {
			return "\x1b[0;1" + rev + "m"
		}
		return "\x1b[0" + rev + "m"
	}
	return ""
}

func rgb(h string) (int, int, int) {
	if len(h) != 7 {
		return 0, 0, 0
	}
	v, _ := strconv.ParseUint(h[1:], 16, 32)
	return int(v >> 16 & 0xff), int(v >> 8 & 0xff), int(v & 0xff)
}

var cube = [6]int{0, 95, 135, 175, 215, 255}

// xterm256 is the nearest colour of the xterm 256-colour palette.
func xterm256(h string) int {
	r, g, b := rgb(h)
	idx := func(v int) int {
		best, bd := 0, 1<<30
		for i, c := range cube {
			if d := (v - c) * (v - c); d < bd {
				best, bd = i, d
			}
		}
		return best
	}
	ri, gi, bi := idx(r), idx(g), idx(b)
	cr, cg, cb := cube[ri], cube[gi], cube[bi]
	cd := (r-cr)*(r-cr) + (g-cg)*(g-cg) + (b-cb)*(b-cb)
	avg := (r + g + b) / 3
	gi2 := min(max((avg-8+5)/10, 0), 23)
	gv := 8 + gi2*10
	gd := (r-gv)*(r-gv) + (g-gv)*(g-gv) + (b-gv)*(b-gv)
	if gd < cd {
		return 232 + gi2
	}
	return 16 + 36*ri + 6*gi + bi
}
