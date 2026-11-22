//go:build linux

package tui

import (
	"context"
	"testing"
)

// v2Hit paints w at 170x45 with Board v2 and returns the first click region
// of kind (and id, when given).
func v2Hit(t *testing.T, w *Workspace, kind boardHitKind, id string) (boardHit, bool) {
	t.Helper()
	w.frameLines(170, 45)
	w.boardHits.mu.Lock()
	defer w.boardHits.mu.Unlock()
	for _, h := range w.boardHits.hits {
		if h.kind == kind && (id == "" || h.id == id) {
			return h, true
		}
	}
	return boardHit{}, false
}

// v2Click clicks the region's first cell.
func v2Click(t *testing.T, w *Workspace, h boardHit) {
	t.Helper()
	if !w.handleBoardMouse(context.Background(), KeyEvent{Type: KeyMouse, MouseRow: h.row + 1, MouseColumn: h.x0 + 1}) {
		t.Fatalf("the Board did not take a click on %+v", h)
	}
}
