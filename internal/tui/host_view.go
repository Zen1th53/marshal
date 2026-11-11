package tui

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/hostwatch"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

// hostPane is the Host view's one pane: the Board's body, between its
// header and footer. The Marshal chat stays docked beside it.
const hostPane layout.PaneID = "host"

// tempColor is the prototype's temperature scale.
func tempColor(t float64) string {
	switch {
	case t >= 90:
		return "failed"
	case t >= 70:
		return "waiting"
	}
	return "merged"
}

// hostFigures is the host figures the vitals row shows.
type hostFigures struct {
	Temp      float64
	TempKnown bool
	Guard     bool
	DiskPct   int
	DiskKnown bool
	LowDisk   bool
}

func (s hostSnapshot) chip() hostFigures {
	var c hostFigures
	if last, ok := s.latest(); ok && last.CPUPackageTempCKnown {
		c.Temp, c.TempKnown = last.CPUPackageTempC, true
	}
	c.Guard = s.Active[ruleThermal]
	if r, ok := s.root(); ok {
		c.DiskPct = int(math.Round(float64(r.UsedBytes) / float64(r.TotalBytes) * 100))
		c.DiskKnown = true
		c.LowDisk = r.FreeBytes < 2*s.Policy.DiskFreeThresholdBytes(r.TotalBytes)
	}
	return c
}

// vitalsKey is the vitals row's host figures as plain text — the rounded
// cpu, ram and temperature, the thermal guard, the disk percent and the
// low-disk flag, unknown as "-" — so the watcher repaints a screen only when
// what the row shows changed.
func (s hostSnapshot) vitalsKey() string {
	cpu := "-"
	if pct, ok := s.cpuBusy(); ok {
		cpu = strconv.Itoa(int(math.Round(pct)))
	}
	ram := "-"
	if last, ok := s.latest(); ok && last.MemTotalBytesKnown && last.MemAvailableBytesKnown && last.MemTotalBytes > 0 {
		ram = strconv.Itoa(int(math.Round(float64(last.MemTotalBytes-last.MemAvailableBytes) / float64(last.MemTotalBytes) * 100)))
	}
	c := s.chip()
	temp := "-"
	if c.TempKnown {
		temp = strconv.Itoa(int(math.Round(c.Temp)))
	}
	disk := "-"
	if c.DiskKnown {
		disk = strconv.Itoa(c.DiskPct)
	}
	return fmt.Sprintf("%s|%s|%s|%v|%s|%v", cpu, ram, temp, c.Guard, disk, c.LowDisk)
}

// hostRender paints the Host view into a w×h body, scrolled down by
// scroll lines. It returns the lines and the furthest useful scroll.
func (v boardVisual) hostRender(s hostSnapshot, w, h, scroll int) ([]string, int) {
	all := v.hostLines(s, w, h)
	maxScroll := max(0, len(all)-h)
	scroll = min(max(0, scroll), maxScroll)
	out := all[scroll:min(len(all), scroll+h)]
	if scroll < maxScroll && len(out) == h && h > 0 {
		out[h-1] = v.fg("dim", fmt.Sprintf("… %d more lines · j/k scroll", maxScroll-scroll))
	}
	for i := range out {
		out[i] = PadCell(out[i], w)
	}
	if !v.unicode() {
		out = hostASCII(out, w)
	}
	return out, maxScroll
}

var hostASCIIReplacer = strings.NewReplacer("°C", "C", "°", "", "·", ".", "→", "->", "−", "-", "×", "x", "█", "#", "░", "-", "│", "|", "▮", "#", "▲", "^", "●", "o", "↓", "v", "↑", "^", "—", "-", "…", ".",
	"▁", ".", "▂", "_", "▃", "-", "▄", "=", "▅", "+", "▆", "*", "▇", "#")

func hostASCII(lines []string, w int) []string {
	out := make([]string, len(lines))
	for i, s := range lines {
		s = hostASCIIReplacer.Replace(s)
		s = strings.Map(func(r rune) rune {
			if r > 127 {
				return '?'
			}
			return r
		}, s)
		out[i] = strings.ReplaceAll(PadCell(s, w), "…", ".")
	}
	return out
}

// hostLines lays the sections out as the prototype does: HOST across the
// top, CPU and DISK beside MEMORY, GPU and NETWORK when there is room for
// two columns, then WATCH POLICY and HOST LOG across the bottom.
func (v boardVisual) hostLines(s hostSnapshot, w, h int) []string {
	var out []string
	add := func(lines []string) {
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, lines...)
	}
	add(v.hostTop(s, w))
	if w >= layout.TwoColumns {
		lw := (w - 2) / 2
		rw := w - 2 - lw
		left := stackSections(v.hostCPU(s, lw), v.hostDisk(s, lw))
		right := stackSections(v.hostMem(s, rw), v.hostGPU(s, rw), v.hostNet(s, rw))
		rows := make([]string, max(len(left), len(right)))
		for i := range rows {
			var l, r string
			if i < len(left) {
				l = left[i]
			}
			if i < len(right) {
				r = right[i]
			}
			rows[i] = PadCell(l, lw) + "  " + PadCell(r, rw)
		}
		add(rows)
	} else {
		add(v.hostCPU(s, w))
		add(v.hostMem(s, w))
		add(v.hostDisk(s, w))
		add(v.hostGPU(s, w))
		add(v.hostNet(s, w))
	}
	add(v.hostPolicy(s, w))
	n := 8
	if rest := h - len(out) - 2; w >= layout.TwoColumns && rest > n {
		n = rest
	}
	add(v.hostLog(s, w, n))
	return out
}

func stackSections(sections ...[]string) []string {
	var out []string
	for i, s := range sections {
		if i > 0 {
			out = append(out, "")
		}
		out = append(out, s...)
	}
	return out
}

func (v boardVisual) kv(label, value string, w int) string {
	return PadCell(v.fg("dim", PadCell(label, 14))+" "+value, w)
}

func joinNonEmpty(sep string, parts ...string) string {
	var keep []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			keep = append(keep, p)
		}
	}
	return strings.Join(keep, sep)
}

// bar is the prototype's gauge: filled to v of top in col, with thin marks.
func (v boardVisual) bar(val, top float64, col string, marks []float64, w int) string {
	if w <= 0 || top <= 0 {
		return ""
	}
	filled := int(math.Round(math.Min(1, math.Max(0, val/top)) * float64(w)))
	at := map[int]bool{}
	for _, m := range marks {
		at[min(w-1, max(0, int(math.Round(m/top*float64(w)))))] = true
	}
	colour := v.code("rule", false) != ""
	var b strings.Builder
	for i := range w {
		under := "rule"
		if i < filled {
			under = col
		}
		switch {
		case at[i] && colour:
			b.WriteString(v.bg(under, v.fg("fg", "│")))
		case at[i]:
			b.WriteString("│")
		case i < filled:
			b.WriteString(v.fg(col, "█"))
		case colour:
			b.WriteString(v.fg("rule", "█"))
		default:
			b.WriteString("░")
		}
	}
	return b.String()
}

// gauge is "label ▕bar▏ suffix" across w cells.
func (v boardVisual) gauge(label string, val, top float64, col string, marks []float64, suffix string, w int) string {
	bw := max(4, w-VisibleLen(label)-VisibleLen(suffix)-2)
	return PadCell(label+" "+v.bar(val, top, col, marks, bw)+" "+suffix, w)
}

func cpuModel(s string) string {
	r := strings.NewReplacer("(R)", "", "(TM)", "", "(tm)", "", " CPU", "", "  ", " ")
	s = strings.TrimSpace(r.Replace(s))
	if i := strings.Index(s, " @ "); i > 0 {
		s = s[:i]
	}
	return s
}

func gpuShort(name string) string {
	return strings.TrimSpace(strings.NewReplacer("NVIDIA ", "", "GeForce ", "").Replace(name))
}

func upFor(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

func hostAgo(d time.Duration) string {
	s := int(d.Seconds())
	switch {
	case s >= 3600:
		return fmt.Sprintf("%dh%02dm ago", s/3600, s%3600/60)
	case s >= 60:
		return fmt.Sprintf("%dm ago", s/60)
	}
	return fmt.Sprintf("%ds ago", max(0, s))
}

func gibF(b uint64) float64 { return float64(b) / (1 << 30) }

func (v boardVisual) hostTop(s hostSnapshot, w int) []string {
	f := s.Facts
	last, _ := s.latest()
	up := ""
	if !f.BootAt.IsZero() {
		up = "up " + upFor(s.Now.Sub(f.BootAt))
	}
	kernel := ""
	if f.Kernel != "" {
		kernel = "kernel " + f.Kernel
	}
	summary := joinNonEmpty(" · ", f.Hostname, f.OS, kernel, up, strings.TrimSpace(f.Iface+" "+f.IP))
	out := []string{v.title("HOST", summary, false, w)}
	cpu := cpuModel(last.CPUModelName)
	if last.CPUCores > 0 {
		cpu = joinNonEmpty(" · ", cpu, fmt.Sprintf("%d cores / %d threads", last.CPUCores, last.CPUThreads))
	}
	if f.MaxGHz > 0 {
		cpu = joinNonEmpty(" · ", cpu, fmt.Sprintf("max %.1f GHz", f.MaxGHz))
	}
	out = append(out, v.kv("CPU", firstNonEmpty(cpu, "—"), w))
	mem := "—"
	if last.MemTotalBytesKnown {
		mem = fmt.Sprintf("%.0f GiB", gibF(last.MemTotalBytes))
		swap := firstNonEmpty(f.Swap, "none")
		mem += " · swap: " + swap
	}
	out = append(out, v.kv("memory", mem, w))
	gpu := "none seen"
	if len(last.GPUs) > 0 {
		g := last.GPUs[0]
		gpu = fmt.Sprintf("%s %.0f GB", g.Name, gibF(g.MemoryTotalBytes))
		if len(last.GPUs) > 1 {
			gpu += fmt.Sprintf(" (+%d more)", len(last.GPUs)-1)
		}
	}
	out = append(out, v.kv("GPU", gpu, w))
	out = append(out, v.kv("storage", firstNonEmpty(f.Storage, "—"), w))
	state := v.fg("merged", "watching")
	if s.Active[ruleThermal] {
		state = v.fg("failed", "thermal guard ACTIVE")
		if s.Guard.Pending && s.Guard.Lowered > 0 {
			state += v.fg("dim", fmt.Sprintf(" · lane cap %d waits in inbox", s.Guard.Lowered))
		}
	}
	parts := []string{state}
	if s.Guard.Cap > 0 {
		parts = append(parts, fmt.Sprintf("lane cap %d", s.Guard.Cap))
	}
	parts = append(parts, "samples every "+shortDur(s.Every), "policy below")
	out = append(out, v.kv("Marshal", strings.Join(parts, " · "), w))
	return out
}

func (v boardVisual) hostCPU(s hostSnapshot, w int) []string {
	last, ok := s.latest()
	avg, avgOK := avgPackage(s.Samples, 30*time.Second)
	if !ok || !last.CPUPackageTempCKnown {
		return []string{v.title("CPU", "no package temperature sensor", false, w)}
	}
	t := last.CPUPackageTempC
	summary := fmt.Sprintf("package %.0f °C", t)
	if avgOK {
		summary += fmt.Sprintf(" · 30 s avg %.1f °C", avg)
	}
	if last.CPULoadAvgKnown {
		summary += fmt.Sprintf(" · load %.2f %.2f %.2f", last.CPULoadAvg1, last.CPULoadAvg5, last.CPULoadAvg15)
	}
	out := []string{v.title("CPU", summary, false, w)}
	big := v.bold(v.fg(tempColor(t), fmt.Sprintf("%.0f°C", t)))
	out = append(out, v.gauge(big, t, 100, tempColor(t), []float64{85, 90}, v.fg("dim", "85 · 90 · 100"), w))
	// Sparkline of the last 40 s.
	bars := []rune("▁▂▃▄▅▆▇█")
	var spark strings.Builder
	hist := s.Samples[max(0, len(s.Samples)-40):]
	for _, x := range hist {
		if !x.CPUPackageTempCKnown {
			spark.WriteString(" ")
			continue
		}
		i := min(7, max(0, int(math.Round((x.CPUPackageTempC-40)/60*7))))
		spark.WriteString(v.fg(tempColor(x.CPUPackageTempC), string(bars[i])))
	}
	out = append(out, PadCell(spark.String(), w))
	out = append(out, PadCell(v.fg("dim", "last 40 s · marks: resume 85, guard 90, crit 100"), w))
	// Per-core temperatures, in core order.
	type core struct {
		n int
		t float64
	}
	var cores []core
	for label, temp := range last.CPUCoreTempC {
		n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(label, "Core")))
		if err != nil {
			n = math.MaxInt
		}
		cores = append(cores, core{n, temp})
	}
	slices.SortFunc(cores, func(a, b core) int { return a.n - b.n })
	if len(cores) > 0 {
		var strip strings.Builder
		lo, hi := cores[0].t, cores[0].t
		for _, c := range cores {
			strip.WriteString(v.fg(tempColor(c.t), "▮"))
			lo, hi = math.Min(lo, c.t), math.Max(hi, c.t)
		}
		out = append(out, PadCell(strip.String()+"  "+v.fg("dim", fmt.Sprintf("%d cores · %.0f–%.0f °C", len(cores), lo, hi)), w))
	}
	freq := "—"
	if last.CPUAverageFreqMHzKnown {
		freq = fmt.Sprintf("%.1f GHz avg", last.CPUAverageFreqMHz/1000)
	}
	out = append(out, v.kv("frequency", freq, w))
	thr := "—"
	if rate, ok := s.throttleRate(); ok && last.CPUPackageThrottleKnown {
		col := ""
		switch {
		case rate > s.Policy.ThrottleRatePerMin:
			col = "failed"
		case rate > 0:
			col = "waiting"
		}
		thr = fmt.Sprintf("%.0f/min · %s since boot", rate, commas(last.CPUPackageThrottleCount))
		if col != "" {
			thr = v.fg(col, thr)
		}
	} else if last.CPUPackageThrottleKnown {
		thr = fmt.Sprintf("— /min · %s since boot", commas(last.CPUPackageThrottleCount))
	}
	out = append(out, v.kv("throttling", thr, w))
	return out
}

func commas(n int64) string {
	s := strconv.FormatInt(n, 10)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 && s[i-1] != '-' {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (v boardVisual) hostMem(s hostSnapshot, w int) []string {
	last, ok := s.latest()
	if !ok || !last.MemTotalBytesKnown || !last.MemAvailableBytesKnown {
		return []string{v.title("MEMORY", "not readable", false, w)}
	}
	total, avail := gibF(last.MemTotalBytes), gibF(last.MemAvailableBytes)
	used := total - avail
	out := []string{v.title("MEMORY", fmt.Sprintf("%.1f / %.0f GiB used · %.1f available", used, total, avail), false, w)}
	pct := used / total * 100
	col := "working"
	if pct > 85 {
		col = "failed"
	}
	out = append(out, v.gauge(PadCell("ram", 8), used, total, col, nil, fmt.Sprintf("%.0f%%", pct), w))
	if last.SwapTotalBytesKnown && last.SwapTotalBytes > 0 {
		sw := gibF(last.SwapTotalBytes - min(last.SwapTotalBytes, last.SwapFreeBytes))
		out = append(out, v.gauge(PadCell("swap", 8), sw, gibF(last.SwapTotalBytes), "verify", nil, fmt.Sprintf("%.1f G", sw), w))
	}
	cache := "—"
	if last.MemCachedBytesKnown {
		cache = fmt.Sprintf("%.0f GiB", gibF(last.MemCachedBytes))
	}
	out = append(out, v.kv("page cache", cache, w))
	pressure := "—"
	if last.MemPressureSomeKnown {
		pressure = fmt.Sprintf("some %.2f · full %.2f", last.MemPressureSomeAvg10Percent, last.MemPressureFullAvg10Percent)
		col := "merged"
		switch {
		case last.MemPressureSomeAvg10Percent >= 40:
			col = "failed"
		case last.MemPressureSomeAvg10Percent >= 10:
			col = "waiting"
		}
		pressure = v.fg(col, pressure)
	}
	out = append(out, v.kv("pressure", pressure, w))
	return out
}

func (v boardVisual) hostDisk(s hostSnapshot, w int) []string {
	r, ok := s.root()
	if !ok {
		return []string{v.title("DISK", "/ not readable", false, w)}
	}
	pct := float64(r.UsedBytes) / float64(r.TotalBytes) * 100
	free := float64(r.FreeBytes) / 1e9
	out := []string{v.title("DISK", fmt.Sprintf("/ %.0f%% used · %.0f GB free", pct, free), false, w)}
	thr := s.Policy.DiskFreeThresholdBytes(r.TotalBytes)
	col := "merged"
	switch {
	case r.FreeBytes < thr:
		col = "failed"
	case r.FreeBytes < 2*thr:
		col = "waiting"
	}
	suffix := fmt.Sprintf("%.0f%%", pct)
	if r.FreeBytes < 2*thr {
		suffix = v.fg("waiting", suffix)
	}
	out = append(out, v.gauge(PadCell("/", 8), float64(r.UsedBytes), float64(r.TotalBytes), col, nil, suffix, w))
	cache := "measuring"
	if s.GoCache.Known {
		cache = fmt.Sprintf("%.0f GB", float64(s.GoCache.Bytes)/1e9)
	}
	if s.Facts.CacheDir != "" {
		cache += " · " + s.Facts.CacheDir
	}
	out = append(out, v.kv("go build cache", cache, w))
	last, _ := s.latest()
	if len(last.NVMeTempC) > 0 {
		keys := make([]string, 0, len(last.NVMeTempC))
		for k := range last.NVMeTempC {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		var temps []string
		for _, k := range keys {
			t := last.NVMeTempC[k]
			temps = append(temps, v.fg(tempColor(t+10), fmt.Sprintf("%.0f °C", t)))
		}
		label := "NVMe"
		if len(keys) > 1 {
			label = fmt.Sprintf("NVMe ×%d", len(keys))
		}
		out = append(out, v.kv(label, strings.Join(temps, " / "), w))
	}
	return out
}

func (v boardVisual) hostGPU(s hostSnapshot, w int) []string {
	last, _ := s.latest()
	if len(last.GPUs) == 0 {
		return []string{v.title("GPU", "none seen · nvidia-smi absent or no NVIDIA GPU", false, w)}
	}
	g := last.GPUs[0]
	out := []string{v.title("GPU", fmt.Sprintf("%s · %.0f °C · %.0f%% busy", gpuShort(g.Name), g.TempC, g.UtilizationPercent), false, w)}
	out = append(out, v.gauge(PadCell("temp", 8), g.TempC, 100, tempColor(g.TempC), []float64{s.Policy.GPUTempThresholdC}, fmt.Sprintf("%.0f°C", g.TempC), w))
	if g.MemoryTotalBytes > 0 {
		out = append(out, v.gauge(PadCell("vram", 8), gibF(g.MemoryUsedBytes), gibF(g.MemoryTotalBytes), "working", nil, fmt.Sprintf("%.1f / %.0f G", gibF(g.MemoryUsedBytes), gibF(g.MemoryTotalBytes)), w))
	}
	return out
}

func (v boardVisual) hostNet(s hostSnapshot, w int) []string {
	last, _ := s.latest()
	iface := firstNonEmpty(last.NetInterface, s.Facts.Iface)
	if iface == "" || !last.NetRxBytesKnown {
		return []string{v.title("NETWORK", "no default route", false, w)}
	}
	out := []string{v.title("NETWORK", strings.TrimSpace(iface+" "+s.Facts.IP), false, w)}
	if s.Net.Known {
		label := "today"
		day := time.Date(s.Net.Since.Year(), s.Net.Since.Month(), s.Net.Since.Day(), 0, 0, 0, 0, s.Net.Since.Location())
		if s.Net.Since.Sub(day) > time.Minute {
			label = "since " + s.Net.Since.Format("15:04")
		}
		out = append(out, v.kv(label, fmt.Sprintf("↓ %s · ↑ %s", decimalSize(s.Net.Rx), decimalSize(s.Net.Tx)), w))
	}
	rx, okRx := s.rate(func(x hostwatch.Sample) (uint64, bool) { return x.NetRxBytes, x.NetRxBytesKnown }, 1)
	tx, okTx := s.rate(func(x hostwatch.Sample) (uint64, bool) { return x.NetTxBytes, x.NetTxBytesKnown }, 1)
	rate := "—"
	if okRx && okTx {
		rate = fmt.Sprintf("↓ %s/s · ↑ %s/s", decimalSize(uint64(rx)), decimalSize(uint64(tx)))
	}
	out = append(out, v.kv("last minute", rate, w))
	return out
}

// hostPolicyRow is one rule of the WATCH POLICY table.
type hostPolicyRow struct {
	rule, action, state, now string
}

func (s hostSnapshot) policyRows() []hostPolicyRow {
	p := s.Policy
	last, ok := s.latest()
	state := func(rule string, warn bool) string {
		switch {
		case s.Active[rule]:
			return "ACTIVE"
		case warn:
			return "WARN"
		}
		return "OK"
	}
	unknown := func(r hostPolicyRow) hostPolicyRow { r.state, r.now = "n/a", "—"; return r }
	var rows []hostPolicyRow

	thermal := hostPolicyRow{rule: fmt.Sprintf("CPU package > %s °C (30 s avg)", trimFloat(p.PackageTempTriggerC)), action: fmt.Sprintf("ask owner: lanes −50%% · restore < %s °C", trimFloat(p.PackageTempReleaseC))}
	if avg, ok := avgPackage(s.Samples, 30*time.Second); ok {
		thermal.state = state(ruleThermal, false)
		if thermal.state == "OK" && avg > p.PackageTempReleaseC {
			thermal.state = "WATCH"
		}
		thermal.now = fmt.Sprintf("%.1f °C", avg)
	} else {
		thermal = unknown(thermal)
	}
	rows = append(rows, thermal)

	disk := hostPolicyRow{
		rule:   fmt.Sprintf("root free < %s GB or %s%%", trimFloat(p.DiskFreeThresholdGB), trimFloat(p.DiskFreeThresholdPercent)),
		action: "notice in footer and log · warn below twice that",
	}
	if r, ok := s.root(); ok {
		disk.state = state(ruleDisk, r.FreeBytes < 2*p.DiskFreeThresholdBytes(r.TotalBytes))
		disk.now = hostwatch.GBText(r.FreeBytes)
	} else {
		disk = unknown(disk)
	}
	rows = append(rows, disk)

	mem := hostPolicyRow{rule: fmt.Sprintf("available < %s GiB", trimFloat(p.MemAvailableThresholdGiB)), action: "notice in footer and log"}
	if ok && last.MemAvailableBytesKnown {
		mem.state = state(ruleMemory, false)
		mem.now = fmt.Sprintf("%.1f GiB", gibF(last.MemAvailableBytes))
	} else {
		mem = unknown(mem)
	}
	rows = append(rows, mem)

	thr := hostPolicyRow{rule: fmt.Sprintf("throttling > %s / min", trimFloat(p.ThrottleRatePerMin)), action: "log for the owner"}
	if rate, ok := s.throttleRate(); ok {
		thr.state = state(ruleThrottle, false)
		thr.now = fmt.Sprintf("%.0f/min", rate)
	} else {
		thr = unknown(thr)
	}
	rows = append(rows, thr)

	gpu := hostPolicyRow{rule: fmt.Sprintf("GPU > %s °C", trimFloat(p.GPUTempThresholdC)), action: "log for the owner"}
	if ok && len(last.GPUs) > 0 {
		hi := last.GPUs[0].TempC
		for _, g := range last.GPUs {
			hi = math.Max(hi, g.TempC)
		}
		gpu.state = state(ruleGPU, false)
		gpu.now = fmt.Sprintf("%.0f °C", hi)
	} else {
		gpu = unknown(gpu)
	}
	rows = append(rows, gpu)

	nvme := hostPolicyRow{rule: fmt.Sprintf("NVMe > %s °C", trimFloat(p.NVMeTempThresholdC)), action: "log for the owner"}
	if ok && len(last.NVMeTempC) > 0 {
		hi := math.Inf(-1)
		for _, t := range last.NVMeTempC {
			hi = math.Max(hi, t)
		}
		nvme.state = state(ruleNVMe, false)
		nvme.now = fmt.Sprintf("%.0f °C", hi)
	} else {
		nvme = unknown(nvme)
	}
	rows = append(rows, nvme)
	return rows
}

func (v boardVisual) hostPolicy(s hostSnapshot, w int) []string {
	out := []string{v.title("WATCH POLICY", "Marshal checks these every second and acts on them", false, w)}
	rest := max(2, w-3-9)
	rw := rest * 6 / 11
	aw := rest - rw
	out = append(out, v.fg("dim", PadCell("   "+PadCell("rule", rw)+PadCell("action", aw)+fmt.Sprintf("%9s", "now"), w)))
	for _, r := range s.policyRows() {
		col := map[string]string{"ACTIVE": "failed", "WARN": "waiting", "WATCH": "waiting", "OK": "merged"}[r.state]
		glyph := "▲"
		switch r.state {
		case "OK":
			glyph = "●"
		case "n/a":
			glyph, col = "·", "dim"
		}
		line := v.fg(col, glyph) + "  " + PadCell(r.rule+" "+v.fg(col, r.state), rw) + PadCell(v.fg("dim", r.action), aw) + fmt.Sprintf("%9s", r.now)
		out = append(out, PadCell(line, w))
	}
	return out
}

func (v boardVisual) hostLog(s hostSnapshot, w, n int) []string {
	out := []string{v.title("HOST LOG", "what Marshal noticed and did", false, w)}
	for _, e := range s.Log[:min(n, len(s.Log))] {
		col := "waiting"
		if e.Kind == "info" {
			col = "accent"
		}
		line := v.fg("dim", fmt.Sprintf("%9s", hostAgo(s.Now.Sub(e.At)))) + " " + v.fg(col, e.Kind) + " " + v.fg("dim", "· "+cleanBoard(e.Msg))
		out = append(out, PadCell(line, w))
	}
	if len(s.Log) == 0 {
		out = append(out, v.fg("dim", PadCell("  nothing yet", w)))
	}
	return out
}
