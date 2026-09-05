package tui

import (
	"testing"
)

func TestCapabilityRegistryFullParity(t *testing.T) {
	reg := GlobalRegistry
	if reg == nil {
		t.Fatalf("GlobalRegistry is nil")
	}

	report := reg.AuditParity()

	// Verify count is 100
	if report.TotalCapabilities != 100 {
		t.Errorf("expected exactly 100 capabilities in registry, got %d", report.TotalCapabilities)
	}

	// Verify ZERO CLI-only remaining
	if report.CLIOnlyRemaining != 0 {
		t.Errorf("expected 0 CLI-only remaining, got %d", report.CLIOnlyRemaining)
	}

	// Verify ZERO Web-only remaining
	if report.WebOnlyRemaining != 0 {
		t.Errorf("expected 0 Web-only remaining, got %d", report.WebOnlyRemaining)
	}

	// Verify all 100 mapped to TUI
	if report.TUIMappedCount != report.TotalCapabilities {
		t.Errorf("expected all %d capabilities mapped to TUI, got %d", report.TotalCapabilities, report.TUIMappedCount)
	}

	// Output formatted report
	formatted := reg.FormatAuditReport()
	t.Logf("\n%s", formatted)

	// Test PaletteActions generation
	actions := reg.ToPaletteActions()
	if len(actions) != report.TotalCapabilities {
		t.Errorf("expected %d palette actions, got %d", report.TotalCapabilities, len(actions))
	}

	// Test specific lookups
	goalEdit, ok := reg.Get("goal.edit")
	if !ok || goalEdit.TUISurface != "/goal edit" {
		t.Errorf("expected goal.edit capability, got ok=%v, cap=%+v", ok, goalEdit)
	}

	doctor, ok := reg.Get("doctor.run")
	if !ok || doctor.TUISurface != "/doctor" {
		t.Errorf("expected doctor.run capability, got ok=%v, cap=%+v", ok, doctor)
	}
}
