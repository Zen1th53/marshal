package tui

import (
	"github.com/Zen1th53/marshal/internal/tui/board/anim"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
)

// animate hands the panel's Board to the animator, if the screen has one.
// Callers hold w.mu.
func (w *Workspace) animate(p *MarshalPanel) {
	a := w.animator.Load()
	if a == nil {
		return
	}
	// Only a Board on screen animates: the classic view gets an empty one,
	// which stops the ticker.
	var b boardmodel.Board
	if p != nil && !w.boardClassic {
		b = p.Board
	}
	a.Update(b)
}

// startAnimator gives the raw terminal a tick source fed with the panel the
// workspace already holds. Animation runs only on a screen that repaints.
// With animation turned off (MARSHAL_REDUCED_MOTION or --no-animation) it is a
// no-op, so the Board's spinners never move.
func (w *Workspace) startAnimator() {
	if w.theme == nil || !w.theme.AnimationEnabled {
		return
	}
	a := anim.New(w.repaint, anim.RealClock{})
	w.mu.Lock()
	w.animator.Store(a)
	w.animate(w.state.Marshal)
	w.mu.Unlock()
}

func (w *Workspace) stopAnimator() {
	if a := w.animator.Swap(nil); a != nil {
		a.Close()
	}
}
