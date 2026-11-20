package tui

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter/providers"
)

// One view serves every registered provider: each model its CLI reported,
// the efforts it advertises, its default and the owner's selection.
func TestProviderCatalogViewForEveryProvider(t *testing.T) {
	catalog := providers.Catalog{Default: "reported-b", Models: []providers.Model{
		{ID: "reported-a", Name: "Reported A"},
		{ID: "reported-b", Efforts: []string{"low", "high"}, Default: true},
	}}
	for _, p := range providers.All() {
		title := strings.ToUpper(p.Spec().Name)
		out := renderProviderCatalog(title, p.Spec().ID, catalog, "reported-a")
		for _, want := range []string{title + " MODELS (2, default: reported-b):", "▶ reported-a", "Reported A", "[SELECTED]", "reported-b", "efforts: low, high", "(default)"} {
			if !strings.Contains(out, want) {
				t.Fatalf("%s view lacks %q:\n%s", p.Spec().ID, want, out)
			}
		}
	}
}
