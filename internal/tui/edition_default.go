//go:build governed

package tui

import "context"

const editionExtreme = false

// Edition reports the build-time MARSHAL edition.
func Edition() string { return "standard" }

// editionLabel leaves the Community wording unchanged.
func editionLabel(text string) string { return text }

// editionHidesSpec reports entries this edition does not offer.
func editionHidesSpec(string) bool { return false }

// readCloudForEdition reads the Community Cloud state.
func (s *StatusSource) readCloudForEdition(ctx context.Context) CloudSnapshot {
	return s.ReadCloud(ctx)
}

// modeFieldLabel names the ULTRA mode row.
const modeFieldLabel = "Entitlement mode"
