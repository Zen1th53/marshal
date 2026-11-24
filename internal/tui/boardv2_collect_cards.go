package tui

import (
	"time"

	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

// collectV2Cards fills the decision card and the paused card of the Board v2 view model. A field left at its zero
// value renders as "not known yet".
//
// Decision is set while the owner must act before the run can move
// (boardv2_cards_decision.go); Paused while a stop waits for one choice
// (boardv2_cards_paused.go). Both read cached state only: what needs a
// store or Git read is read once in the background (boardv2_cards_state.go).
// Their keys are handleV2CardKey (boardv2_cards_keys.go).
func (w *Workspace) collectV2Cards(b *vm.Board) {
	now := b.Now
	if now.IsZero() {
		now = time.Now()
	}
	if d, ok := w.v2DecisionNow(now); ok && !(b.NowLine.ChatAsked && d.kind == decisionArchitecture) {
		card := d.card
		card.Revision, card.Choices = w.decisionRevision(d), decisionChoices(d)
		b.Decision = &card
	}
	if z, ok := w.v2PauseNow(now); ok {
		card := z.card
		b.Paused = &card
	}
}
