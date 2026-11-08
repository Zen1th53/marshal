package tui

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
)

// The Marshal model had no menu entry: only typing /marshal model <provider>
// changed it. The command palette now lists one "Choose Marshal model" row per
// provider this edition's Marshal accepts, and the chosen row runs that same
// command, so the choice is persisted exactly as the typed command does.
func TestPaletteChoosesMarshalModel(t *testing.T) {
	var listed []string
	for _, action := range GlobalRegistry.ToPaletteActions() {
		if !strings.HasPrefix(action.Title, "Choose Marshal model: ") {
			continue
		}
		provider := strings.TrimPrefix(action.Title, "Choose Marshal model: ")
		if action.Command != "/marshal model "+provider {
			t.Fatalf("%s runs %q, not /marshal model %s", action.Title, action.Command, provider)
		}
		listed = append(listed, provider)
	}
	if !slices.Equal(listed, app.MarshalProviders()) {
		t.Fatalf("palette lists Marshal models %v, want %v", listed, app.MarshalProviders())
	}
	if describeCompletion("/marshal") != "Marshal" {
		t.Fatalf("/marshal completion is described as %q", describeCompletion("/marshal"))
	}

	pal := NewCommandPalette(nil, GlobalRegistry.ToPaletteActions())
	pal.Open()
	for _, r := range "choose marshal model" {
		pal.HandleKey(KeyEvent{Type: KeyRune, Rune: r})
	}
	if got := len(pal.filtered); got != len(app.MarshalProviders()) {
		t.Fatalf("searching the palette shows %d rows, want one per provider", got)
	}
	pal.HandleKey(KeyEvent{Type: KeyDown})
	selected, ok := pal.HandleKey(KeyEvent{Type: KeyEnter})
	if !ok || selected == nil || selected.Command != "/marshal model claude" {
		t.Fatalf("palette selection: %v %+v", ok, selected)
	}

	marshalSwitchEnvironment(t)
	w := NewWorkspace(nil, "project", "test-session")
	w.workDir = t.TempDir()
	if _, err := w.ExecuteCommand(context.Background(), selected.Command); err != nil {
		t.Fatalf("%s: %v", selected.Command, err)
	}
	if got := loadDefaultProvider(w.providerRoot()); got != "claude" {
		t.Fatalf("palette choice persisted %q, want claude", got)
	}
}
