//go:build !governed

package tui

import (
	"context"
	"strings"
)

const editionExtreme = true

// Edition reports the build-time MARSHAL edition.
func Edition() string { return "Marshal" }

// editionLabel rewrites Community wording for this edition's display.
func editionLabel(text string) string {
	return strings.NewReplacer(
		"MARSHAL — COMMUNITY TUI", "Marshal",
		"Community Cloud & ULTRA", "Local ULTRA",
		"Community Cloud / ULTRA", "Local ULTRA",
	).Replace(text)
}

// editionHidesSpec reports entries that only make sense with the Community
// Cloud. ULTRA is local in Extreme, so there is nothing to request.
func editionHidesSpec(specID string) bool {
	switch specID {
	case "CTUI-0035", "CTUI-0157", "CTUI-0557", "CTUI-0562":
		return true
	}
	return false
}

// readCloudForEdition replaces the Cloud reading: ULTRA is local in Extreme, so
// there is no connection, entitlement or lease to report.
func (s *StatusSource) readCloudForEdition(context.Context) CloudSnapshot {
	return extremeCloudSnapshot()
}

// modeFieldLabel names the ULTRA mode row: nothing is entitled in Extreme.
const modeFieldLabel = "Mode"
