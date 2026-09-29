package app

import (
	"sort"
	"strings"

	"github.com/Zen1th53/marshal/internal/harness"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/router"
)

// ModelInventory combines static profiles with model names from existing caches.
// A nil cache means discovery data is unavailable.
func ModelInventory(codexCachedModels, ollamaCachedTags []string) marshal.Inventory {
	inventory := marshal.Inventory{Profiles: router.DefaultProfiles()}
	seen := make(map[string]bool)
	for _, p := range inventory.Profiles {
		seen[p.Provider+"/"+p.Model] = true
	}
	add := func(provider, name string) {
		name = strings.TrimSpace(name)
		if name == "" || seen[provider+"/"+name] {
			return
		}
		seen[provider+"/"+name] = true
		inventory.Profiles = append(inventory.Profiles, router.ModelProfile{Provider: provider, Model: name, Capabilities: []string{"code", "reasoning"}, CostClass: "MEDIUM", LatencyClass: "MEDIUM", Available: true})
	}
	profiles := harness.NewIntelligence().DefaultProfiles()
	for _, key := range []string{"codex", "claude-code", "opencode", "antigravity"} {
		p, ok := profiles[key]
		if !ok {
			inventory.MissingSources = append(inventory.MissingSources, "harness:"+key)
			continue
		}
		provider := key
		if key == "claude-code" {
			provider = "claude"
		}
		if key == "antigravity" {
			provider = "gemini"
		}
		for _, name := range p.SupportedModels {
			add(provider, name)
		}
	}
	for _, alias := range []string{"opus", "sonnet", "haiku"} {
		add("claude", alias)
	}
	if codexCachedModels == nil {
		inventory.MissingSources = append(inventory.MissingSources, "codex debug models cache")
	} else {
		for _, name := range codexCachedModels {
			add("codex", name)
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
