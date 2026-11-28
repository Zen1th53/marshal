//go:build !governed

package marshal

// VerifierAttributionGap reports whether native identity can be observed.
func VerifierAttributionGap(provider string) string {
	switch provider {
	case "claude", "codex", "opencode", "agy":
		return ""
	}
	return "native model attribution unavailable"
}
