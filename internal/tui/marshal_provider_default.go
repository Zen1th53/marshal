//go:build governed

package tui

const marshalProviderUsage = "codex|claude|agy"

const marshalProviderNames = "codex, claude or agy"

func marshalResumeArgs(provider, session string) []string {
	return resumeArgsForProvider(provider, session)
}
