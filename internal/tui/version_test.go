package tui

import (
	"strings"
	"testing"
)

func TestDevelopmentVersionBanner(t *testing.T) {
	if BuildVersion != "dev" {
		t.Fatalf("local build version = %q, want dev", BuildVersion)
	}
	screen := RenderStyledScreen(UIState{}, NewTheme(ThemeNoColor, false, false), 160)
	if !strings.Contains(screen, "MARSHAL dev CONTROL PLANE") {
		t.Fatalf("missing development version in banner: %s", screen)
	}
}
