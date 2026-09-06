package tui

import (
	"regexp"

	"github.com/Zen1th53/marshal/internal/auth"
)

var (
	bearerPattern = regexp.MustCompile(`(?i)Bearer\s+[a-zA-Z0-9_\-\.]{10,}`)
	skPattern     = regexp.MustCompile(`sk-[a-zA-Z0-9_\-]{16,}`)
	// Sensitivity follows the key, not the length of the value. Requiring eight
	// or more characters let short secrets through in cleartext -- an audit
	// observed "password=hunter2" reaching the store unredacted. One character
	// is enough to match, while an empty value is left alone so ordinary prose
	// such as "token: " is not mangled.
	keyPattern = regexp.MustCompile(`(?i)\b(api[_-]?key|secret|token|password|passwd|access[_-]?token|refresh[_-]?token|client[_-]?secret|authorization|auth[_-]?token|private[_-]?key)\s*[:=]\s*["']?([^"'\s]+)["']?`)
)

// RedactContent scrubs sensitive tokens, keys, and patterns from text before rendering in TUI.
func RedactContent(input string, knownSecrets []string) string {
	if len(knownSecrets) > 0 {
		b := auth.RedactSecrets([]byte(input), knownSecrets)
		input = string(b)
	}

	input = bearerPattern.ReplaceAllString(input, "Bearer [REDACTED]")
	input = skPattern.ReplaceAllString(input, "sk-[REDACTED]")
	input = keyPattern.ReplaceAllString(input, "$1: [REDACTED]")

	return input
}
