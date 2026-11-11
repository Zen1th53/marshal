//go:build governed

package tui

const editionExtreme = false

// Edition reports the build-time MARSHAL edition.
func Edition() string { return "standard" }

// editionLabel leaves the Community wording unchanged.
func editionLabel(text string) string { return text }
