package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
)

// Changing the Marshal is now one key and one picker (m, action marshal.pick),
// not one palette row per provider. The per-provider "Choose Marshal model"
// rows stay registered for completion and the parity audit, but the palette
// hides them so the picker is the single way to change the model.
func TestPaletteChoosesMarshalModel(t *testing.T) {
	// The capability rows are still registered, one per provider this edition
	// accepts.
	var registered []string
	for _, action := range GlobalRegistry.ToPaletteActions() {
		if strings.HasPrefix(action.ID, "marshal.model.") {
			registered = append(registered, strings.TrimPrefix(action.ID, "marshal.model."))
		}
	}
	if !slices.Equal(registered, app.MarshalProviders()) {
		t.Fatalf("registry defines Marshal-model rows %v, want %v", registered, app.MarshalProviders())
	}
	if describeCompletion("/marshal") != "Marshal" {
		t.Fatalf("/marshal completion is described as %q", describeCompletion("/marshal"))
	}

	// The palette the Board shows lists the Marshal picker and none of the
	// capability rows.
	w, _ := boardWorkspace(t)
	defer w.Close()
	actions := append(boardPaletteActions(w), w.registryPaletteActions()...)
	var pick *PaletteAction
	for i := range actions {
		a := &actions[i]
		if strings.HasPrefix(a.ID, "marshal.model.") {
			t.Fatalf("the palette lists a Marshal-model capability row %q", a.ID)
		}
		if a.ID == "marshal.pick" {
			pick = a
		}
	}
	if pick == nil {
		t.Fatal("the palette does not list the Marshal picker (marshal.pick)")
	}
	if pick.Title != "change the Marshal" {
		t.Fatalf("marshal.pick title %q, want %q", pick.Title, "change the Marshal")
	}
}
