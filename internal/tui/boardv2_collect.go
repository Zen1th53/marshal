package tui

import "github.com/Zen1th53/marshal/internal/tui/board/vm"

// boardV2 builds one frame of the Board v2 view model from the workspace.
// Each collector owns its own fields; none reads another's output.
func (w *Workspace) boardV2() vm.Board {
	var b vm.Board
	w.collectV2Flow(&b)
	w.collectV2Marshal(&b)
	w.collectV2Chat(&b)
	w.collectV2Cards(&b)
	b.Web = w.webState()
	return b
}
