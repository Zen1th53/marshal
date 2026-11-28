package tui

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/board/vm"
)

// Before the host watcher's first sample the figures are unknown (-1), not
// 0%; a newer release and an MCP failure are sent as facts, the failure's
// text is not.
func TestCollectV2System(t *testing.T) {
	w := &Workspace{}
	var b vm.Board
	w.collectV2System(&b)
	if s := b.System; s.CPUPct != -1 || s.MemPct != -1 || s.DiskPct != -1 || s.TempC != -1 || s.ThermalGuard || s.UpdateAvailable != "" || s.MCPFailed {
		t.Fatalf("empty workspace: %+v", s)
	}
	w.setUpdateAvailable("v9.9.9")
	w.noteMCPFailure("restore failed: /secret/path/socket refused")
	w.collectV2System(&b)
	if b.System.UpdateAvailable != "v9.9.9" || !b.System.MCPFailed {
		t.Fatalf("update and MCP: %+v", b.System)
	}
}
