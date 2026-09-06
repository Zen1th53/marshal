package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

// TestRegistrySurfacesAreWellFormed rejects surface strings the palette,
// completion engine, and command dispatcher cannot parse. A capability whose
// TUISurface names several commands at once parses as a single unknown command,
// so the entry advertises a control that does not exist.
func TestRegistrySurfacesAreWellFormed(t *testing.T) {
	for _, cap := range GlobalRegistry.All() {
		surface := strings.TrimSpace(cap.TUISurface)
		if surface == "" {
			t.Errorf("capability %s has no TUI surface", cap.ID)
			continue
		}
		if !strings.HasPrefix(surface, "/") {
			continue
		}
		name := strings.Fields(surface)[0]
		if strings.ContainsAny(name, ",;|") {
			t.Errorf("capability %s surface %q packs several commands into one entry; register one capability per command",
				cap.ID, surface)
		}
	}
}

// TestEveryRegisteredCommandIsDispatched is the fast guard behind the PTY
// parity test: every slash command the registry advertises must be handled by
// the dispatcher rather than falling through to the unknown-command branch.
//
// This runs in-process so it fails in seconds during development; the PTY suite
// proves the same property through a real terminal.
func TestEveryRegisteredCommandIsDispatched(t *testing.T) {
	ctx := context.Background()

	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "parity.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := st.InitProject(ctx, model.Project{
		ID:            "PROJECT-parity",
		Repository:    "/repo/parity",
		DefaultBranch: "main",
		PackVersion:   "6.0.0",
	}); err != nil {
		t.Fatalf("init project: %v", err)
	}

	ws := NewWorkspace(st, "PROJECT-parity", "sess-parity")

	seen := map[string]bool{}
	for _, cap := range GlobalRegistry.All() {
		surface := strings.TrimSpace(cap.TUISurface)
		if !strings.HasPrefix(surface, "/") {
			continue
		}
		name := strings.Fields(surface)[0]
		if seen[name] {
			continue
		}
		seen[name] = true

		out, err := ws.ExecuteCommand(ctx, name)
		if err != nil {
			// An error is acceptable: it means the command reached a real
			// handler and that handler reported a genuine condition.
			continue
		}
		if strings.Contains(out, fmt.Sprintf("Unknown command %q", name)) {
			t.Errorf("capability %s advertises %s, which the dispatcher does not handle", cap.ID, name)
		}
	}

	if len(seen) == 0 {
		t.Fatal("registry advertises no slash commands")
	}
}

// TestNoFabricatedModelsInDiscovery proves participant discovery never invents a
// model name. On a host where a harness binary is absent the model must be
// UNAVAILABLE, and where it is present but unconfigured it must be UNKNOWN --
// never a plausible-looking identifier the probe cannot have established.
func TestNoFabricatedModelsInDiscovery(t *testing.T) {
	fabricated := []string{
		"claude-3-7-sonnet", "claude-3-5-sonnet",
		"gpt-4o", "o3-mini", "o1",
		"deepseek-coder",
		"gemini-2.5-pro", "gemini-2.5-flash",
	}

	for _, p := range DiscoverTeamParticipants(nil) {
		for _, bad := range fabricated {
			if p.Model == bad {
				t.Errorf("participant %s reports fabricated model %q; expected %s or %s",
					p.AgentID, bad, UnknownModel, UnavailableModel)
			}
		}
	}

	// The probe itself must assert no model list.
	for _, pr := range ProbeHarnesses() {
		if len(pr.Models) != 0 {
			t.Errorf("probe for %s asserted models %v; the probe cannot read a harness's configured model",
				pr.HarnessName, pr.Models)
		}
	}
}
