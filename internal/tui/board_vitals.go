package tui

import (
	"fmt"
	"math"
	"strings"

	"github.com/Zen1th53/marshal/internal/tui/board/model"
	"github.com/Zen1th53/marshal/internal/usage"
)

// running is the one "N running" figure the TASKS header, the LANES title
// and the vitals row all show: the tasks being moved now (working, in
// review or verifying, as their rows spin), or the live lanes when those
// count more. A review has no worker lane, the Marshal reviews it, and it is
// running all the same.
func (v boardVisual) running() int {
	run, _, _ := v.counts()
	if v.board.Run.Running > 0 {
		run = v.board.Run.Running
	}
	return max(run, v.liveLanes())
}

// vitalsData is what the vitals row paints, gathered off the paint path.
type vitalsData struct {
	quotas    []usage.Quota // last reading; nil before the first
	read      bool
	installed map[string]bool // nil until loadFirstUse has run
}

// liveLanes is the number of lanes doing work: the run's own count when it
// reports one, otherwise the lanes that are neither idle nor parked. The
// header and the vitals row show the same figure.
func (v boardVisual) liveLanes() int {
	if v.board.Run.ActiveLanes > 0 {
		return v.board.Run.ActiveLanes
	}
	n := 0
	for _, l := range v.board.Lanes {
		if l.State != model.LaneIdle && l.State != model.LaneParked {
			n++
		}
	}
	return n
}

// vitalsLeft is one provider's piece of the vitals row's left block. state is
// 0 full, 1 without the reset, 2 short (glyph and percent), 3 dropped;
// unavail groups the unavailable and not-read parts, which give way before
// the known ones and have no reset to drop (mid is full).
type vitalsLeft struct {
	id               string // "limit:<provider>"
	full, mid, short string
	unavail          bool
	state            int
}

// text is the part as its state paints it, "" once dropped.
func (p vitalsLeft) text() string {
	switch p.state {
	case 0:
		return p.full
	case 1:
		return p.mid
	case 2:
		return p.short
	}
	return ""
}

// vitalsLine paints the vitals row w cells wide and where each part landed:
// "limit:<provider>", "host", "lanes".
func (v boardVisual) vitalsLine(w int) (string, []headerSpan) {
	sep := v.fg("faint", " · ")
	dash, ellip, degC := "—", "…", "°C"
	if !v.unicode() {
		dash, ellip, degC = "-", ".", "C"
	}

	// Left block: each installed, profiled or read provider's glyph and usage.
	var left []vitalsLeft
	for _, p := range []string{"claude", "codex", "agy", "opencode"} {
		var q usage.Quota
		found := false
		for _, x := range v.vitals.quotas {
			if x.Provider == p {
				q, found = x, true
				break
			}
		}
		known := false
		if found {
			if _, ok := q.Binding(); ok {
				known = true
			}
		}
		if !v.vitals.installed[p] && v.health[p] == "" && !known {
			continue
		}
		glyphColor := providerStateColour[v.health[p]]
		if glyphColor == "" {
			glyphColor = "dim"
		}
		mk := v.mark(p, glyphColor)
		switch {
		case !v.vitals.read:
			full := mk + v.fg("dim", p+" "+ellip)
			left = append(left, vitalsLeft{"limit:" + p, full, full, mk + v.fg("dim", ellip), true, 0})
		case !known:
			full := mk + v.fg("dim", p+" "+limitUnknown)
			// Every provider whose limit cannot be read says so the same
			// way: "limit unknown" in full, "?" when the row is narrow.
			left = append(left, vitalsLeft{"limit:" + p, full, full, mk + v.fg("dim", "?"), true, 0})
		default:
			fig, pct, _ := limitFigure(q, v.now)
			bar := limitBar(pct, v.unicode())
			figColor := limitColour(pct)
			barColor := figColor
			suffix := ""
			if q.Stale {
				figColor, barColor = "dim", "dim"
				if age := readAgo(q, v.now); age != "" {
					suffix = v.fg("dim", " ("+age+")")
				}
			}
			full := mk + v.fg("dim", p) + " " + v.fg(barColor, bar) + " " + v.fg(figColor, fig) + suffix
			// The narrower forms keep the "left" label: the reset goes
			// first, then the name and the bar, so a narrow row still says
			// which quantity it shows.
			shortFig, _, _ := strings.Cut(fig, " ·")
			mid := mk + v.fg("dim", p) + " " + v.fg(barColor, bar) + " " + v.fg(figColor, shortFig) + suffix
			short := mk + v.fg(figColor, shortFig) + suffix
			left = append(left, vitalsLeft{"limit:" + p, full, mid, short, false, 0})
		}
	}

	// Right block: cpu, temperature, ram, disk and the running count.
	var cpuTxt, tempTxt, ramTxt, diskTxt string
	tempKnown, tempGuard, diskKnown, diskLow, ramHigh := false, false, false, false, false
	if v.host != nil {
		cpuTxt = v.fg("dim", "cpu ") + dash
		if pct, ok := v.host.cpuBusy(); ok {
			col := ""
			switch {
			case pct >= 90:
				col = "failed"
			case pct >= 70:
				col = "waiting"
			}
			cpuTxt = v.fg("dim", "cpu ") + v.fg(col, fmt.Sprintf("%.0f%%", pct))
		}
		c := v.host.chip()
		if c.TempKnown {
			tempKnown = true
			tempTxt = v.fg(tempColor(c.Temp), fmt.Sprintf("%.0f%s", c.Temp, degC))
			if c.Guard {
				tempGuard = true
				tempTxt += " " + v.fg("failed", "GUARD")
			}
		}
		ramTxt = v.fg("dim", "ram ") + dash
		if last, ok := v.host.latest(); ok && last.MemTotalBytesKnown && last.MemAvailableBytesKnown && last.MemTotalBytes > 0 {
			pct := math.Round(float64(last.MemTotalBytes-last.MemAvailableBytes) / float64(last.MemTotalBytes) * 100)
			col := ""
			switch {
			case pct >= 90:
				col = "failed"
			case pct >= 80:
				col = "waiting"
			}
			ramHigh = pct >= 80
			ramTxt = v.fg("dim", "ram ") + v.fg(col, fmt.Sprintf("%.0f%%", pct))
		}
		if c.DiskKnown {
			diskKnown, diskLow = true, c.LowDisk
			col := "dim"
			if c.LowDisk {
				col = "waiting"
			}
			diskTxt = v.fg(col, fmt.Sprintf("disk %d%%", c.DiskPct))
		}
	}
	n := v.running()
	runColor := ""
	if n > 0 {
		runColor = "working"
	}
	runFull := v.fg(runColor, fmt.Sprint(n)) + v.fg("dim", " running")
	runShort := v.fg(runColor, fmt.Sprint(n)) + v.fg("dim", " run")

	// rseg is one right-block segment with the span group it belongs to.
	type rseg struct {
		group string // "host" or "lanes"
		text  string
	}
	dropRam, dropDisk, dropTemp, shortRun := false, false, false, false
	rightSegs := func() []rseg {
		var out []rseg
		if v.host != nil {
			out = append(out, rseg{"host", cpuTxt})
			if tempKnown && !dropTemp {
				out = append(out, rseg{"host", tempTxt})
			}
			if !dropRam {
				out = append(out, rseg{"host", ramTxt})
			}
			if diskKnown && !dropDisk {
				out = append(out, rseg{"host", diskTxt})
			}
		}
		run := runFull
		if shortRun {
			run = runShort
		}
		return append(out, rseg{"lanes", run})
	}

	joinLeft := func() string {
		var parts []string
		for _, p := range left {
			if t := p.text(); t != "" {
				parts = append(parts, t)
			}
		}
		return strings.Join(parts, sep)
	}
	joinRight := func() string {
		segs := rightSegs()
		texts := make([]string, len(segs))
		for i, s := range segs {
			texts[i] = s.text
		}
		return strings.Join(texts, sep)
	}
	fits := func() bool {
		ll, rl := VisibleLen(joinLeft()), VisibleLen(joinRight())
		if ll == 0 {
			return 2+rl <= w
		}
		return 1+ll+2+rl+1 <= w
	}
	// reduce applies one give-way step, most-expendable first; it returns
	// false when nothing else can give way (the line is then cut to w).
	// The order is the rule a narrow terminal follows (100 columns keeps
	// every limit): unavailable names, then each known limit's reset, the
	// temperature, each known limit's name and bar, the unavailable parts,
	// the "running" word, the disk (kept while low), the ram (kept from 80%
	// used), and last the known limits right to left, the first always kept.
	shift := func(unavail bool, from, to int) bool {
		for i := len(left) - 1; i >= 0; i-- {
			if left[i].unavail == unavail && left[i].state >= from && left[i].state < to {
				left[i].state = to
				return true
			}
		}
		return false
	}
	// The unavailable parts give way together, so every provider whose
	// limit cannot be read reads the same at any width.
	shiftAll := func(from, to int) bool {
		moved := false
		for i := range left {
			if left[i].unavail && left[i].state >= from && left[i].state < to {
				left[i].state, moved = to, true
			}
		}
		return moved
	}
	reduce := func() bool {
		if shiftAll(0, 2) || shift(false, 0, 1) { // 1-2. unavailable names, resets
			return true
		}
		if tempKnown && !dropTemp && !tempGuard { // 3. temperature, unless GUARD
			dropTemp = true
			return true
		}
		if shift(false, 1, 2) || shiftAll(2, 3) { // 4-5. bars, unavailable parts
			return true
		}
		if !shortRun { // 6. shorten running
			shortRun = true
			return true
		}
		if diskKnown && !dropDisk && !diskLow { // 7. disk, unless low
			dropDisk = true
			return true
		}
		if v.host != nil && !dropRam && !ramHigh { // 8. ram, unless high
			dropRam = true
			return true
		}
		kept := 0 // 9. drop known right to left, keeping the first
		for _, p := range left {
			if p.state != 3 {
				kept++
			}
		}
		if kept > 1 {
			for i := len(left) - 1; i >= 0; i-- {
				if !left[i].unavail && left[i].state != 3 {
					left[i].state = 3
					return true
				}
			}
		}
		return false
	}
	for !fits() && reduce() {
	}

	leftStr := joinLeft()
	segs := rightSegs()
	rightStr := joinRight()
	ll, rl := VisibleLen(leftStr), VisibleLen(rightStr)
	rx := w - 1 - rl

	// Assemble: one leading space, the left block, the gap, the right block
	// flush right, one trailing space. The gap is at least two cells.
	var line string
	if ll == 0 {
		line = strings.Repeat(" ", max(0, rx)) + rightStr + " "
	} else {
		gap := max(2, w-2-ll-rl)
		line = " " + leftStr + strings.Repeat(" ", gap) + rightStr + " "
	}

	var spans []headerSpan
	x := 1
	first := true
	for _, p := range left {
		t := p.text()
		if t == "" {
			continue
		}
		if !first {
			x += VisibleLen(sep)
		}
		first = false
		n := VisibleLen(t)
		spans = append(spans, headerSpan{p.id, x, x + n})
		x += n
	}
	hostX0, hostX1 := -1, -1
	sx := rx
	for i, s := range segs {
		if i > 0 {
			sx += VisibleLen(sep)
		}
		n := VisibleLen(s.text)
		if s.group == "host" {
			if hostX0 < 0 {
				hostX0 = sx
			}
			hostX1 = sx + n
		} else {
			spans = append(spans, headerSpan{"lanes", sx, sx + n})
		}
		sx += n
	}
	if hostX0 >= 0 {
		spans = append(spans, headerSpan{"host", hostX0, hostX1})
	}
	return PadCell(line, w), spans
}
