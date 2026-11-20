package app

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter/providers"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/router"
)

// The inventory is what the installed CLIs reported, and nothing else: no
// static profile, no alias list, and no CLI is started to build it.
func TestModelInventoryListsOnlyReportedModels(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "invoked")
	script := "#!/bin/sh\ntouch " + marker + "\nexit 1\n"
	for _, name := range []string{"claude", "codex", "agy", "opencode"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	inventory := ModelInventory(map[string]providers.Catalog{
		"codex":  {Models: []providers.Model{{ID: "reported-codex"}}},
		"claude": {Models: []providers.Model{{ID: "reported-alias"}, {ID: "reported-alias"}}},
	}, nil)
	if _, err := marshal.Recommend(marshal.GoalAssessment{}, inventory, router.RouteRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("a provider CLI was invoked: %v", err)
	}
	var got []string
	for _, p := range inventory.Profiles {
		got = append(got, p.Provider+"/"+p.Model)
	}
	if want := []string{"codex/reported-codex", "claude/reported-alias"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("profiles = %v, want %v", got, want)
	}
	if want := []string{"agy models", "ollama /api/tags cache", "opencode models"}; !reflect.DeepEqual(inventory.MissingSources, want) {
		t.Fatalf("missing = %v, want %v", inventory.MissingSources, want)
	}
}
