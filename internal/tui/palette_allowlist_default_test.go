//go:build governed

package tui

import "testing"

// The governed edition's palette keeps the capability registry after the
// Board's keys, without the rows the Board already lists by their key, and
// can quit.
func TestGovernedPaletteKeepsRegistry(t *testing.T) {
	rows := map[string]bool{}
	for _, a := range append(boardPaletteActions(nil), paletteRegistryActions()...) {
		rows[a.ID] = true
	}
	for _, id := range []string{"goal.create", "claim.list", "evidence.list", "budget.view", "team.list", "doctor.run"} {
		if !rows[id] {
			t.Errorf("the governed palette dropped %s", id)
		}
	}
	if rows["update.check"] || !rows["fkey.update"] {
		t.Error("the governed palette lists update twice or not at all; F10's Board row is the one row")
	}
	if rows["app.doctor"] || !rows["app.quit"] {
		t.Error("the governed palette lists the health check twice (doctor.run is its row) or cannot quit")
	}
}
