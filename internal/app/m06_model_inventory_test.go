package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/router"
)

func TestModelInventoryNeverInvokesClaude(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "invoked")
	script := "#!/bin/sh\ntouch " + marker + "\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	inventory := ModelInventory(nil, nil)
	if _, err := marshal.Recommend(marshal.GoalAssessment{}, inventory, router.RouteRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("claude was invoked: %v", err)
	}
	found := false
	for _, p := range inventory.Profiles {
		if p.Provider == "claude" && p.Model == "opus" {
			found = true
		}
	}
	if !found {
		t.Fatal("static Claude aliases missing")
	}
}
