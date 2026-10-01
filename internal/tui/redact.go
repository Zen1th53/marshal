package tui

import "github.com/Zen1th53/marshal/internal/redaction"

// RedactContent scrubs sensitive content with the shared project helper.
func RedactContent(input string, knownSecrets []string) string {
	return redaction.RedactContent(input, knownSecrets)
}
