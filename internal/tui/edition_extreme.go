//go:build !governed

package tui

import (
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
