package tui

import (
	"math"

	"github.com/Zen1th53/marshal/internal/board/vm"
)

// collectV2System fills b.System: the host figures the vitals row shows,
// the thermal guard, a newer release and whether MCP failed. Everything is
// in memory (the host watcher's ring, the update check's tag, the MCP
// fault); nothing is probed here. The MCP failure's text stays in the
// terminal (F6): only the fact that it failed is sent.
func (w *Workspace) collectV2System(b *vm.Board) {
	s := vm.System{CPUPct: -1, MemPct: -1, DiskPct: -1, TempC: -1}
	if h := w.hostWatch.Load(); h != nil {
		snap := h.snapshot()
		if pct, ok := snap.cpuBusy(); ok {
			s.CPUPct = int(math.Round(pct))
		}
		if last, ok := snap.latest(); ok && last.MemTotalBytesKnown && last.MemAvailableBytesKnown && last.MemTotalBytes > 0 {
			s.MemPct = int(math.Round(float64(last.MemTotalBytes-last.MemAvailableBytes) / float64(last.MemTotalBytes) * 100))
		}
		c := snap.chip()
		if c.TempKnown {
			s.TempC = int(math.Round(c.Temp))
		}
		if c.DiskKnown {
			s.DiskPct, s.LowDisk = c.DiskPct, c.LowDisk
		}
		s.ThermalGuard = c.Guard || snap.Guard.Active
		// Only an owner-approved temporary cap is in effect; an undecided
		// or declined request leaves the cap as configured.
		s.ThermalCap = snap.Guard.Temporary
		s.Notice = snap.Notice
	}
	w.mu.RLock()
	s.UpdateAvailable = w.state.UpdateAvailable
	w.mu.RUnlock()
	s.MCPFailed = w.mcpFailed()
	b.System = s
}
