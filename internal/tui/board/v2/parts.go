package v2

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

// dash is what an unknown value reads as.
const dash = "—"

var (
	sInk   = st{}
	sBold  = st{b: true}
	sDim   = st{fg: cDim}
	sFaint = st{fg: cFaint}
	sAcc   = st{fg: cAcc}
	sOK    = st{fg: cOK}
	sWarn  = st{fg: cWarn}
	sBad   = st{fg: cBad}
	sVio   = st{fg: cVio}
)

func tone(t vm.Tone) string {
	switch t {
	case vm.ToneOK:
		return cOK
	case vm.ToneWarn:
		return cWarn
	case vm.ToneBad:
		return cBad
	case vm.ToneInfo:
		return cAcc
	case vm.ToneMuted:
		return cFaint
	case vm.ToneVio:
		return cVio
	}
	return cInk
}

// chip is bold text on a coloured ground, a space either side.
func chip(text, bg, fg string) seg {
	if fg == "" {
		fg = cDark
	}
	return seg{" " + text + " ", st{fg: fg, bg: bg, b: true}}
}

// button is a secondary action: ink on a dark key cap.
func button(text string) seg { return seg{" " + text + " ", st{fg: cInk, bg: cKey}} }

// gauge is n cells of █ for pct and ░ for the rest; an unknown pct is all ░.
func gauge(pct int, color string, n int) []seg {
	if pct < 0 {
		return []seg{{strings.Repeat("░", n), sFaint}}
	}
	k := int(float64(min(pct, 100))/100*float64(n) + 0.5)
	return []seg{{strings.Repeat("█", k), st{fg: color}}, {strings.Repeat("░", n-k), sFaint}}
}

var sparkRunes = []rune("▁▂▃▄▅▆▇█")

// spark is a sparkline of vals scaled to their maximum.
func spark(vals []int) string {
	hi := 0
	for _, v := range vals {
		hi = max(hi, v)
	}
	var b strings.Builder
	for _, v := range vals {
		i := 0
		if hi > 0 {
			i = max(0, v) * (len(sparkRunes) - 1) / hi
		}
		b.WriteRune(sparkRunes[i])
	}
	return b.String()
}

// progress is the done / active / waiting bar, n cells.
func progress(p vm.Plan, n int) []seg {
	den := max(p.Total, p.Done+p.Active+p.Waiting)
	if den <= 0 {
		return []seg{{strings.Repeat("░", n), sFaint}}
	}
	d := (p.Done*n*2 + den) / (den * 2)
	a := ((p.Done+p.Active)*n*2+den)/(den*2) - d
	d, a = min(d, n), min(a, n-min(d, n))
	return []seg{{strings.Repeat("█", d), sOK}, {strings.Repeat("█", a), sAcc}, {strings.Repeat("░", n-d-a), sFaint}}
}

// dur reads a duration the way the Board does: 8s, 14m, 1m40s, 2h5m.
func dur(d time.Duration) string {
	if d <= 0 {
		return dash
	}
	s := int(d.Round(time.Second) / time.Second)
	switch {
	case s < 60:
		return strconv.Itoa(s) + "s"
	case s < 3600:
		if s%60 == 0 {
			return strconv.Itoa(s/60) + "m"
		}
		return fmt.Sprintf("%dm%ds", s/60, s%60)
	default:
		if s/60%60 == 0 {
			return strconv.Itoa(s/3600) + "h"
		}
		return fmt.Sprintf("%dh%dm", s/3600, s/60%60)
	}
}

func clock(t time.Time) string {
	if t.IsZero() {
		return dash
	}
	return t.Format("15:04")
}

func tokens(n int64) string {
	switch {
	case n <= 0:
		return dash
	case n >= 1_000_000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1e6), ".0") + "M"
	case n >= 1000:
		return strconv.FormatInt((n+500)/1000, 10) + "k"
	}
	return strconv.FormatInt(n, 10)
}

func or(s, alt string) string {
	if strings.TrimSpace(s) == "" {
		return alt
	}
	return s
}

func glyph(provider string) string {
	switch provider {
	case "claude":
		return "✳"
	case "codex":
		return "⬡"
	case "opencode":
		return "◇"
	}
	return "◆"
}

// cliName is the provider CLI's own name, for the chat frame.
func cliName(provider string) string {
	switch provider {
	case "claude":
		return "Claude Code"
	case "codex":
		return "Codex"
	case "agy":
		return "Antigravity"
	case "opencode":
		return "OpenCode"
	}
	return or(provider, "Marshal chat")
}

// modelName is how a model id reads in prose: opus-5.5 is Opus 5.5.
func modelName(id string) string {
	if id == "" {
		return dash
	}
	parts := strings.Split(id, "-")
	for i, p := range parts {
		if p != "" {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

// topBar is row 0: brand and version, crumbs, the state chip, the Marshal
// segment with its m key, and the meters on the right.
func topBar(c *canvas, y int, b vm.Board) {
	t := b.Top
	c.fill(0, y, c.w, 1, cTop)
	top := func(s st) st { s.bg = cTop; return s }
	x := c.put(1, y, " MARSHAL ", top(sBold))
	x = c.put(x, y, or(t.Version, dash), top(sFaint))
	x = c.put(x, y, "   ", top(sInk))
	crumbs := [][2]string{{"project", or(t.Project, dash)}}
	if t.Run != "" {
		crumbs = append(crumbs, [2]string{"run", t.Run})
	}
	var meters []string
	if t.Workers > 0 {
		meters = append(meters, strconv.Itoa(t.Workers)+" workers")
	}
	meters = append(meters, "inbox "+strconv.Itoa(t.Inbox))
	ck := t.Clock
	if ck.IsZero() {
		ck = b.Now
	}
	meters = append(meters, clock(ck))
	m := strings.Join(meters, "  ") + " "
	mx := c.w - width(m) - 1

	mk := b.Marshal
	prov := or(mk.Provider, dash)
	segs := []seg{
		{glyph(mk.Provider) + " ", st{fg: cAcc, bg: cSeg}},
		{prov + " · " + or(mk.Model, dash), st{fg: cInk, bg: cSeg, b: true}},
	}
	if mk.Effort != "" {
		segs = append(segs, seg{" · " + mk.Effort + " ", st{fg: cDim, bg: cSeg}})
	} else {
		segs = append(segs, seg{" ", st{bg: cSeg}})
	}
	segs = append(segs, seg{" m ", st{fg: cInk, bg: cSegKey, b: true}})
	ln := 1
	for _, s := range segs {
		ln += width(s.t)
	}
	state := or(t.State, dash)
	// Space for the crumbs: drop the run, then the meters, when narrow.
	need := func() int {
		n := x
		for i, cr := range crumbs {
			if i > 0 {
				n += 3
			}
			n += width(cr[0]) + 1 + width(cr[1])
		}
		return n + 2 + width(state) + 2
	}
	segX := mx - ln - 2
	if need() > segX-1 && len(crumbs) > 1 {
		crumbs = crumbs[:1]
	}
	if need() > segX-1 {
		m, mx = clock(ck)+" ", c.w-width(clock(ck))-2
		segX = mx - ln - 2
	}
	if need() > segX-1 {
		segX = -1 // no room for the Marshal segment
	}
	for i, cr := range crumbs {
		if i > 0 {
			x = c.put(x, y, " / ", top(sFaint))
		}
		x = c.put(x, y, cr[0]+" ", top(sDim))
		x = c.put(x, y, cr[1], top(sBold))
	}
	c.put(x+2, y, " "+state+" ", st{fg: cDark, bg: tone(t.StateTone), b: true})
	if t.StateTone == vm.ToneNone {
		c.put(x+2, y, " "+state+" ", st{fg: cDark, bg: cDim, b: true})
	}
	c.put(mx, y, m, top(sDim))
	if segX >= 0 {
		c.segsClip(segX, y, append([]seg{{" ", st{bg: cSeg}}}, segs...), mx)
	}
}

// nowStrip is row 1: what the Marshal does, what the work does and what is
// needed from the owner.
func nowStrip(c *canvas, y int, n vm.NowLine) {
	c.fill(0, y, c.w, 1, cBg)
	mw, ww := 34, 30
	if c.w < 132 {
		mw, ww = 34*c.w/132, 30*c.w/132
	}
	x := c.put(0, y, " NOW ", st{fg: cAcc, bg: cNow, b: true})
	x = c.put(x, y, " marshal ", sFaint)
	x = c.put(x, y, fit(or(n.Marshal, dash), mw), sInk)
	x = c.put(x, y, "│", st{fg: cRule})
	x = c.put(x, y, " work ", sFaint)
	x = c.put(x, y, fit(or(n.Work, dash), ww), sInk)
	bg, fg := cYouOK, cOK
	if !n.FromYouOK {
		bg, fg = cYouW, cWarn
	}
	c.fill(x, y, c.w-x, 1, bg)
	x = c.put(x, y, " from you ", st{fg: cFaint, bg: bg})
	c.put(x, y, or(n.FromYou, dash), st{fg: fg, bg: bg, b: true})
}

// keyBar is the row of key hints.
func keyBar(c *canvas, y int, keys []vm.Key) {
	x := 1
	for i, k := range keys {
		if x+width(k.Key)+2 > c.w {
			break
		}
		x0 := x
		x = c.put(x, y, " "+k.Key+" ", st{fg: cInk, bg: cKey, b: true})
		x = c.put(x+1, y, k.Label, sFaint)
		c.hit(y, x0, x, HitKey, i, k.Key)
		x += 2
	}
}

// chatStrip is the MARSHAL CHAT strip of the chat screens.
func chatStrip(c *canvas, x0, y, w int, b vm.Board, compact bool) {
	c.fill(x0, y, w, 1, cStrip)
	s := func(fg string, bold bool) st { return st{fg: fg, bg: cStrip, b: bold} }
	mk := b.Marshal
	end := x0 + w
	bound := seg{"● live", s(cOK, true)}
	if !b.Chat.Bound {
		bound = seg{"○ not bound", s(cWarn, true)}
	}
	if compact {
		model := glyph(mk.Provider) + " " + or(mk.Model, dash)
		if mk.Effort != "" {
			model += " · " + mk.Effort
		}
		in := "  input clear"
		if !b.Chat.InputClear {
			in = "  input waiting"
		}
		c.segsClip(x0, y, []seg{chip("MARSHAL CHAT", cAcc, ""), {" " + model + "  ", s(cInk, true)}, bound, {in, s(cDim, false)}}, end)
		return
	}
	model := glyph(mk.Provider) + " " + or(mk.Provider, dash) + " · " + or(mk.Model, dash)
	if mk.Effort != "" {
		model += " · " + mk.Effort
	}
	c.segsClip(x0, y, []seg{chip("MARSHAL CHAT", cAcc, ""), {" ", s(cInk, false)}, {model, s(cInk, true)},
		{"  │ ", s(cRule, false)}, {"bound to run ", s(cDim, false)}, bound, {"  │ ", s(cRule, false)},
		{"stage ", s(cDim, false)}, {or(b.Chat.Stage, dash), s(cVio, false)}}, end)
	in := "input clear"
	if !b.Chat.InputClear {
		in = "input waiting"
	}
	right := fmt.Sprintf("%d requests answered · %s ", b.Chat.Answered, in)
	if rx := x0 + w - width(right) - 1; rx > x0+60 {
		c.putClip(rx, y, right, s(cDim, false), end)
	}
}
