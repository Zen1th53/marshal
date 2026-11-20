package app

import (
	"sort"
	"strings"

	providerspec "github.com/Zen1th53/marshal/internal/adapter/provider"
	"github.com/Zen1th53/marshal/internal/adapter/providers"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/router"
)

// ModelInventory lists the models each provider's installed CLI reported
// through providers.Provider.Models (catalogs, keyed by provider ID) and the
// Ollama tags from its cache. Nothing is listed that a CLI did not report: a
// provider without a catalog is a missing source, never a fixed list.
func ModelInventory(catalogs map[string]providers.Catalog, ollamaCachedTags []string) marshal.Inventory {
	var inventory marshal.Inventory
	seen := make(map[string]bool)
	add := func(provider, name string) {
		name = strings.TrimSpace(name)
		if name == "" || seen[provider+"/"+name] {
			return
		}
		seen[provider+"/"+name] = true
		inventory.Profiles = append(inventory.Profiles, router.ModelProfile{Provider: provider, Model: name, Capabilities: []string{"code", "reasoning"}, CostClass: "MEDIUM", LatencyClass: "MEDIUM", Available: true})
	}
	for _, spec := range providerspec.All() {
		catalog, ok := catalogs[spec.ID]
		if !ok {
			inventory.MissingSources = append(inventory.MissingSources, spec.ID+" models")
			continue
		}
		for _, m := range catalog.Models {
			add(spec.ID, m.ID)
		}
	}
	if ollamaCachedTags == nil {
		inventory.MissingSources = append(inventory.MissingSources, "ollama /api/tags cache")
	} else {
		for _, name := range ollamaCachedTags {
			add("ollama", name)
		}
	}
	sort.Strings(inventory.MissingSources)
	return inventory
}
