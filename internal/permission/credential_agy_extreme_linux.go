//go:build !governed && linux

package permission

// Native AGY has its own project credential consent. Gemini grants and native
// discovery aliases never substitute for this exact operator decision object.
func privateCredentialDisplayName(provider string) (string, bool) {
	if provider == "agy" {
		return "Antigravity", true
	}
	return "", false
}
