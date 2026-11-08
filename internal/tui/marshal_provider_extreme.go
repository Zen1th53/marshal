//go:build !governed

package tui

const marshalProviderUsage = "codex|claude|agy|opencode"

const marshalProviderNames = "codex, claude, agy or opencode"

func marshalResumeArgs(provider, session string) []string {
	if provider == "opencode" {
		if session == "" {
			return nil
		}
		return []string{"--session", session}
	}
	return resumeArgsForProvider(provider, session)
}
